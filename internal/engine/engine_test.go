package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gitfleet/internal/gitrepo"
)

type fakeGit struct {
	mu               sync.Mutex
	repos            map[string]gitrepo.Repository
	active           map[string]int
	concurrent, peak int
	overlap          bool
	started          chan string
	release          chan struct{}
	calls            []string
}

func (f *fakeGit) Inspect(_ context.Context, path string) gitrepo.Repository { return f.repos[path] }

func (f *fakeGit) Operate(ctx context.Context, path string, _ gitrepo.Action, output func(string)) (string, error) {
	f.mu.Lock()
	key := f.repos[path].CommonDir
	f.active[key]++
	f.concurrent++
	f.peak = max(f.peak, f.concurrent)
	if f.active[key] > 1 {
		f.overlap = true
	}
	f.calls = append(f.calls, path)
	f.mu.Unlock()
	defer func() { f.mu.Lock(); defer f.mu.Unlock(); f.active[key]--; f.concurrent-- }()
	f.started <- path
	select {
	case <-f.release:
	case <-ctx.Done():
		return "cancelled", ctx.Err()
	}
	output("progress")
	if path == "bad" {
		return "failed output", errors.New("simulated rejection")
	}
	return "done", nil
}

func TestBatchConcurrencySerializationAndPartialFailure(t *testing.T) {
	f := &fakeGit{repos: map[string]gitrepo.Repository{}, active: map[string]int{}, started: make(chan string, 20), release: make(chan struct{})}
	var repos []gitrepo.Repository
	for i := 0; i < 8; i++ {
		path := fmt.Sprint(i)
		if i == 7 {
			path = "bad"
		}
		key := path
		if i < 2 {
			key = "shared"
		}
		r := gitrepo.Repository{Path: path, CommonDir: key}
		repos = append(repos, r)
		f.repos[path] = r
	}
	events := Batch(context.Background(), repos, gitrepo.Fetch, f)
	for i := 0; i < Workers; i++ {
		select {
		case <-f.started:
		case <-time.After(3 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	close(f.release)
	finished, failed := 0, 0
	for e := range events {
		if e.Kind == "finished" {
			finished++
			if e.Err != nil {
				failed++
			}
		}
	}
	if finished != 8 || failed != 1 {
		t.Fatalf("results: %d finished, %d failed", finished, failed)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.overlap || f.peak != Workers {
		t.Fatalf("overlap=%v peak=%d", f.overlap, f.peak)
	}
}

func TestBatchCancellationDoesNotStartQueuedWork(t *testing.T) {
	r1 := gitrepo.Repository{Path: "one", CommonDir: "shared"}
	r2 := gitrepo.Repository{Path: "two", CommonDir: "shared"}
	f := &fakeGit{repos: map[string]gitrepo.Repository{"one": r1, "two": r2}, active: map[string]int{}, started: make(chan string, 2), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := Batch(ctx, []gitrepo.Repository{r1, r2}, gitrepo.Pull, f)
	select {
	case <-f.started:
	case <-time.After(3 * time.Second):
		t.Fatal("did not start")
	}
	cancel()
	finished := 0
	for e := range events {
		if e.Kind == "finished" {
			finished++
			if !errors.Is(e.Err, context.Canceled) {
				t.Fatalf("expected cancellation: %v", e.Err)
			}
		}
	}
	if finished != 2 || len(f.calls) != 1 {
		t.Fatalf("finished %d, calls %v", finished, f.calls)
	}
}

type statusGit struct {
	mu           sync.Mutex
	active, peak int
	calls        []string
	started      chan string
	release      chan struct{}
}

func (g *statusGit) Inspect(ctx context.Context, path string) gitrepo.Repository {
	g.mu.Lock()
	g.active++
	g.peak = max(g.peak, g.active)
	g.calls = append(g.calls, path)
	g.mu.Unlock()
	defer func() { g.mu.Lock(); g.active--; g.mu.Unlock() }()
	g.started <- path
	select {
	case <-g.release:
	case <-ctx.Done():
	}
	r := gitrepo.Repository{Path: path, Branch: "updated"}
	if path == "missing" {
		r.Error = "repository no longer exists"
	}
	return r
}

func (*statusGit) Operate(context.Context, string, gitrepo.Action, func(string)) (string, error) {
	panic("status refresh must not run network operations")
}

func TestRefreshInspectsOnlyKnownPathsWithFourWorkers(t *testing.T) {
	g := &statusGit{started: make(chan string, 16), release: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// These paths need not exist: refresh must directly inspect them, not discover.
	paths := []string{"one", "two", "three", "four", "five", "six", "missing", "one"}
	events := Refresh(ctx, paths, g)
	for i := 0; i < Workers; i++ {
		select {
		case <-g.started:
		case <-ctx.Done():
			t.Fatal("status workers did not start in parallel")
		}
	}
	close(g.release)
	seen := make(map[string]bool)
	for e := range events {
		if e.Kind != "repository" {
			t.Fatalf("unexpected refresh event: %s", e.Kind)
		}
		if seen[e.Repository.Path] {
			t.Fatal("duplicate status result")
		}
		seen[e.Repository.Path] = true
		if e.Repository.Path == "missing" && e.Repository.Error == "" {
			t.Fatal("missing repository error discarded")
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(seen) != 7 || len(g.calls) != 7 || g.peak != Workers {
		t.Fatalf("results=%v calls=%v concurrency=%d", seen, g.calls, g.peak)
	}
}

func TestRefreshCancellationStopsQueuedInspections(t *testing.T) {
	g := &statusGit{started: make(chan string, 16), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := Refresh(ctx, []string{"one", "two", "three", "four", "five", "six"}, g)
	for i := 0; i < Workers; i++ {
		select {
		case <-g.started:
		case <-time.After(3 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	cancel()
	for range events {
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.calls) != Workers {
		t.Fatalf("queued refresh ran after cancellation: %v", g.calls)
	}
}
