// Package engine runs discovery and Git jobs independently of terminal rendering.
package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"gitfleet/internal/gitrepo"
)

const Workers = 4

type Client interface {
	Inspect(context.Context, string) gitrepo.Repository
	Operate(context.Context, string, gitrepo.Action, func(string)) (string, error)
}

type Event struct {
	Kind       string
	Path       string
	Repository gitrepo.Repository
	Output     string
	Err        error
}

func Scan(ctx context.Context, roots []string, git Client, ignored map[string]bool) <-chan Event {
	return inspectRepositories(ctx, git, ignored, func(found func(string), warning func(error)) {
		gitrepo.Discover(ctx, roots, found, warning)
	})
}

// Refresh only inspects the supplied working trees; it never walks scan roots.
func Refresh(ctx context.Context, paths []string, git Client) <-chan Event {
	paths = append([]string(nil), paths...)
	return inspectRepositories(ctx, git, nil, func(found func(string), _ func(error)) {
		for _, path := range paths {
			if ctx.Err() != nil {
				return
			}
			found(path)
		}
	})
}

// Both discovery and direct refresh feed the same bounded status worker pool.
func inspectRepositories(ctx context.Context, git Client, ignored map[string]bool, produce func(func(string), func(error))) <-chan Event {
	events := make(chan Event, 64)
	go func() {
		defer close(events)
		jobs := make(chan string, Workers)
		var wg sync.WaitGroup
		for i := 0; i < Workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for path := range jobs {
					if ctx.Err() != nil {
						continue
					}
					check, cancel := context.WithTimeout(ctx, 30*time.Second)
					r := git.Inspect(check, path)
					cancel()
					select {
					case events <- Event{Kind: "repository", Repository: r}:
					case <-ctx.Done():
					}
				}
			}()
		}
		seen := make(map[string]bool)
		produce(func(path string) {
			if seen[path] {
				return
			}
			seen[path] = true
			if ignored[path] {
				select {
				case events <- Event{Kind: "repository", Repository: gitrepo.Repository{Path: path}}:
				case <-ctx.Done():
				}
				return
			}
			select {
			case jobs <- path:
			case <-ctx.Done():
			}
		}, func(err error) {
			select {
			case events <- Event{Kind: "warning", Err: err}:
			case <-ctx.Done():
			}
		})
		close(jobs)
		wg.Wait()
	}()
	return events
}

// Batch assigns each common Git directory to one worker, so linked worktrees
// cannot mutate shared refs concurrently. Independent repositories run in parallel.
func Batch(ctx context.Context, repos []gitrepo.Repository, action gitrepo.Action, git Client) <-chan Event {
	events := make(chan Event, 64)
	go func() {
		defer close(events)
		var groups [][]gitrepo.Repository
		indices := make(map[string]int)
		for _, r := range repos {
			key := r.CommonDir
			if key == "" {
				key = r.Path
			}
			i, ok := indices[key]
			if !ok {
				i = len(groups)
				indices[key] = i
				groups = append(groups, nil)
			}
			groups[i] = append(groups[i], r)
		}
		jobs := make(chan []gitrepo.Repository)
		var wg sync.WaitGroup
		for i := 0; i < Workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for group := range jobs {
					for _, r := range group {
						if ctx.Err() != nil {
							events <- Event{Kind: "finished", Path: r.Path, Repository: r, Err: ctx.Err()}
							continue
						}
						if r.Error != "" {
							events <- Event{Kind: "finished", Path: r.Path, Repository: r, Err: fmt.Errorf("repository unavailable: %s", r.Error)}
							continue
						}
						events <- Event{Kind: "started", Path: r.Path}
						out, err := git.Operate(ctx, r.Path, action, func(s string) {
							// Output is a complete tail snapshot; dropping intermediate
							// updates under load loses no final output.
							select {
							case events <- Event{Kind: "output", Path: r.Path, Output: s}:
							default:
							}
						})
						refresh, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
						updated := git.Inspect(refresh, r.Path)
						cancel()
						events <- Event{Kind: "finished", Path: r.Path, Repository: updated, Output: out, Err: err}
					}
				}
			}()
		}
		for _, group := range groups {
			jobs <- group
		}
		close(jobs)
		wg.Wait()
	}()
	return events
}
