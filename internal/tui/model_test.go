package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"gitfleet/internal/engine"
	"gitfleet/internal/gitrepo"
	"gitfleet/internal/ignore"
)

type testGit struct{}

func (testGit) Inspect(_ context.Context, path string) gitrepo.Repository {
	return gitrepo.Repository{Path: path, CommonDir: path, Branch: "main"}
}
func (testGit) Operate(_ context.Context, _ string, _ gitrepo.Action, _ func(string)) (string, error) {
	return "success", nil
}

func fixture() *Model {
	m := New(context.Background(), []string{"/repos"}, testGit{}, nil)
	m.repos = []gitrepo.Repository{
		{Path: "/repos/a", CommonDir: "a", Branch: "main", Upstream: "origin/main", Ahead: 1, TrackingKnown: true},
		{Path: "/repos/b", CommonDir: "b", Branch: "feature", Untracked: 2},
		{Path: "/repos/c", CommonDir: "c", Branch: "main", Upstream: "origin/main", Behind: 1, TrackingKnown: true},
	}
	m.refreshOutput(true)
	return m
}

// Persistence requires absolute paths in the host platform's native format.
func persistenceFixture(t *testing.T) *Model {
	t.Helper()
	m := fixture()
	root := t.TempDir()
	for i, name := range []string{"a", "b", "c"} {
		m.repos[i].Path = filepath.Join(root, name)
	}
	m.roots = []string{root}
	m.refreshOutput(true)
	return m
}

func key(m *Model, s string) tea.Cmd {
	msg := tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
	switch s {
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		msg = tea.KeyPressMsg{Code: tea.KeyTab}
	}
	_, cmd := m.Update(msg)
	return cmd
}

func drain(m *Model) {
	if m.running {
		drainBatch(m)
		return
	}
	for e := range m.events {
		m.handleEvent(eventMsg{event: e, open: true})
	}
	m.handleEvent(eventMsg{open: false})
}

func drainBatch(m *Model) {
	for e := range m.batchEvents {
		m.Update(batchMsg{eventMsg{event: e, open: true}})
	}
	m.Update(batchMsg{eventMsg{open: false}})
}

func TestTargetingSelectionAndHiddenRows(t *testing.T) {
	m := fixture()
	if got := m.targets(false); len(got) != 1 || got[0].Path != "/repos/a" {
		t.Fatalf("focus target: %v", got)
	}
	key(m, " ")
	key(m, "j")
	key(m, " ")
	m.search.SetValue("feature")
	if got := m.targets(false); len(got) != 2 {
		t.Fatalf("hidden selection lost: %v", got)
	}
	if got := m.targets(true); len(got) != 3 {
		t.Fatalf("all should ignore filtering: %v", got)
	}
	if !strings.Contains(safeOutput(m.View().Content), "2 selected (1 hidden)") {
		t.Fatal("hidden selection not disclosed")
	}
	key(m, "esc")
	if got := m.targets(false); len(got) != 1 || got[0].Path != "/repos/b" {
		t.Fatalf("focused filtered target: %v", got)
	}
	key(m, "a")
	if len(m.selected) != 1 || !m.selected["/repos/b"] {
		t.Fatal("select visible affected hidden rows")
	}
}

func TestImmediateActionsAndNoOverlap(t *testing.T) {
	m := fixture()
	if cmd := key(m, "P"); cmd == nil || !m.running || m.total != 3 {
		t.Fatal("Push All did not start immediately")
	}
	if cmd := key(m, "f"); cmd != nil || m.batchAction != gitrepo.Push {
		t.Fatal("overlapping batch started")
	}
	drain(m)
	if m.running || m.completed != 3 || m.failed != 0 {
		t.Fatalf("batch completion: %+v", m)
	}
	if m.results["/repos/a"].output != "success" {
		t.Fatal("missing final output")
	}
}

func TestFetchAllCountersStayStableWhileScrolling(t *testing.T) {
	m := fixture()
	for i := 0; i < 40; i++ {
		path := fmt.Sprintf("/repos/extra-%02d", i)
		m.repos = append(m.repos, gitrepo.Repository{Path: path, CommonDir: path})
	}
	count := len(m.repos)
	key(m, "F")
	scroll := func() {
		for _, button := range []tea.MouseButton{tea.MouseWheelDown, tea.MouseWheelUp} {
			m.Update(tea.MouseWheelMsg{X: 10, Y: 8, Button: button})
			key(m, "j")
			key(m, "k")
			m.View()
		}
		if len(m.repos) != count || m.total != count || m.completed > count {
			t.Fatalf("scroll changed counters: repos=%d total=%d completed=%d; want %d", len(m.repos), m.total, m.completed, count)
		}
		if m.buttons()[3].label != fmt.Sprintf("[F Fetch All %d]", count) {
			t.Fatal("Fetch All button count changed")
		}
	}
	for event := range m.batchEvents {
		m.handleBatchEvent(eventMsg{event: event, open: true})
		scroll()
	}
	m.handleBatchEvent(eventMsg{open: false})
	for i := 0; i < 10; i++ {
		scroll()
	}
	if m.running || m.completed != count {
		t.Fatalf("fetch did not finish: %d/%d", m.completed, count)
	}
}

func TestScanProgressDoesNotChangeActionButtonTotals(t *testing.T) {
	m := New(context.Background(), []string{"/repos"}, testGit{}, nil)
	m.scanning = true
	m.cancel = func() {}
	m.seen = make(map[string]bool)
	m.events = make(chan engine.Event)
	for i := 0; i < 20; i++ {
		path := fmt.Sprintf("/repos/%02d", i)
		m.handleEvent(eventMsg{open: true, event: engine.Event{Kind: "repository", Repository: gitrepo.Repository{Path: path}}})
		key(m, "j")
		if got := m.buttons()[3].label; got != "[F Fetch found]" {
			t.Fatalf("partial scan count appeared in action button: %s", got)
		}
		if !strings.Contains(m.status, fmt.Sprintf("%d repositories found so far", i+1)) {
			t.Fatalf("scan progress missing: %s", m.status)
		}
	}
	if cmd := key(m, "F"); cmd == nil || !m.running || m.total != 20 {
		t.Fatal("fetch did not start on repositories found so far")
	}
	drainBatch(m)
	m.handleEvent(eventMsg{open: false})
	if got := m.buttons()[3].label; got != "[F Fetch All 20]" {
		t.Fatalf("final total missing: %s", got)
	}
	for i := 0; i < 40; i++ {
		key(m, "k")
		m.View()
	}
	if got := m.buttons()[3].label; got != "[F Fetch All 20]" {
		t.Fatalf("scroll changed final total: %s", got)
	}
}

func TestMouseSelectAndActionButton(t *testing.T) {
	m := fixture()
	m.click(1, 6) // second row checkbox
	if !m.selected["/repos/b"] {
		t.Fatal("mouse selection failed")
	}
	rows, detail := m.layout()
	if cmd := m.click(2, 7+rows+detail); cmd == nil || !m.running || m.total != 1 {
		t.Fatal("fetch button did not dispatch selection")
	}
	drain(m)
	if m.results["/repos/b"].action != gitrepo.Fetch {
		t.Fatal("wrong mouse action")
	}
}

func TestSearchDoesNotDispatchActionKeys(t *testing.T) {
	m := fixture()
	key(m, "/")
	key(m, "p")
	if m.running || m.search.Value() != "p" {
		t.Fatal("search key dispatched an action")
	}
	key(m, "enter")
	if m.search.Focused() {
		t.Fatal("search still focused")
	}
}

func TestScanPreservesSelectionAndDropsRemovedRepositories(t *testing.T) {
	m := fixture()
	m.results["/repos/a"] = result{action: gitrepo.Pull, state: "failed", err: "old failure"}
	m.selected["/repos/a"] = true
	m.selected["/repos/c"] = true
	m.scanning = true
	m.cancel = func() {}
	m.seen = map[string]bool{}
	m.events = make(chan engine.Event)
	m.handleEvent(eventMsg{open: true, event: engine.Event{Kind: "repository", Repository: m.repos[0]}})
	m.handleEvent(eventMsg{open: true, event: engine.Event{Kind: "repository", Repository: m.repos[1]}})
	m.handleEvent(eventMsg{open: false})
	if len(m.repos) != 2 || len(m.selected) != 1 || !m.selected["/repos/a"] {
		t.Fatal("refresh did not preserve valid selection")
	}
	if _, ok := m.results["/repos/a"]; ok {
		t.Fatal("scan retained the previous operation result")
	}
}

func TestResizeAndExpandedViewStayWithinTerminal(t *testing.T) {
	m := fixture()
	for _, size := range [][2]int{{70, 20}, {80, 24}, {120, 40}, {160, 50}} {
		for _, expanded := range []bool{false, true} {
			m.details = expanded
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			lines := strings.Split(m.View().Content, "\n")
			if len(lines) != size[1] {
				t.Fatalf("%v expanded=%v: rendered %d lines", size, expanded, len(lines))
			}
			for _, line := range lines {
				if lipgloss.Width(line) > size[0] {
					t.Fatalf("line wider than %d: %q", size[0], line)
				}
			}
		}
	}
}

func TestExpandingKeepsFocusedRowVisible(t *testing.T) {
	m := fixture()
	for i := 0; i < 30; i++ {
		m.repos = append(m.repos, gitrepo.Repository{Path: fmt.Sprintf("/repos/extra-%02d", i)})
	}
	m.cursor = 25
	m.clamp()
	key(m, "enter")
	rows, _ := m.layout()
	if m.cursor < m.offset || m.cursor >= m.offset+rows {
		t.Fatal("expanding hid the focused row")
	}
}

func TestUntrustedTextCannotControlTerminal(t *testing.T) {
	input := "name\n\x1b[31mred\x1b[0m\x1b]52;c;secret\x07"
	if out := safe(input); strings.ContainsAny(out, "\x1b\n\x07") {
		t.Fatalf("unsafe label %q", out)
	}
	if out := safeOutput(input); strings.ContainsAny(out, "\x1b\x07") || !strings.Contains(out, "\n") {
		t.Fatalf("unsafe output %q", out)
	}
}

func TestFilters(t *testing.T) {
	m := fixture()
	m.repos = append(m.repos,
		gitrepo.Repository{Path: "/repos/diverged", Ahead: 1, Behind: 2},
		gitrepo.Repository{Path: "/repos/conflicted", Conflicts: 1},
		gitrepo.Repository{Path: "/repos/clean"},
	)
	for filter, want := range map[int][]string{
		changedFilter: {"/repos/b", "/repos/conflicted"},
		syncFilter:    {"/repos/a", "/repos/c", "/repos/diverged"},
		ignoredFilter: {},
	} {
		m.filter = filter
		var got []string
		for _, row := range m.visible() {
			got = append(got, row.Path)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("filter %d: got %v; want %v", filter, got, want)
		}
	}
}

func TestIgnorePersistsAndExcludesBulkTargets(t *testing.T) {
	m := persistenceFixture(t)
	a, b, c := m.repos[0].Path, m.repos[1].Path, m.repos[2].Path
	path := filepath.Join(t.TempDir(), "ignored.json")
	store, err := ignore.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m.ignored = store
	m.selected[a], m.selected[c] = true, true
	key(m, "i")
	if len(m.visible()) != 1 || len(m.selected) != 0 {
		t.Fatal("ignored repos remained visible or selected")
	}
	if targets := m.targets(true); len(targets) != 1 || targets[0].Path != b {
		t.Fatalf("bulk targets include ignored repos: %v", targets)
	}
	if m.buttons()[3].label != "[F Fetch All 1]" {
		t.Fatal("Fetch All includes ignored count")
	}
	// A fresh application instance honors the persisted paths.
	reloaded, err := ignore.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	next := fixture()
	next.repos = append([]gitrepo.Repository(nil), m.repos...)
	next.ignored = reloaded
	if len(next.visible()) != 1 {
		t.Fatal("ignore did not survive restart")
	}
	key(next, "I")
	if len(next.visible()) != 2 {
		t.Fatal("ignored repos cannot be found for restoration")
	}
	key(next, "a")
	if len(next.targets(false)) != 0 {
		t.Fatal("selected ignored repos can be fetched")
	}
	if cmd := key(next, "f"); cmd != nil || next.running {
		t.Fatal("fetch started on ignored repos")
	}
	key(next, "I")
	if len(next.selected) != 0 {
		t.Fatal("ignored selection leaked into normal view")
	}
}

func TestRestoreIgnoredRepoRefreshesStatus(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	store, err := ignore.Load(filepath.Join(root, "ignored.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set([]string{path}, true); err != nil {
		t.Fatal(err)
	}
	m := New(context.Background(), []string{root}, testGit{}, store)
	m.Init()
	drain(m)
	if len(m.visible()) != 0 || len(m.repos) != 1 || m.repos[0].Branch != "" {
		t.Fatal("ignored repo should be listed without inspection")
	}
	key(m, "I")
	// Exercise the clickable Restore button too.
	buttons := m.buttons()
	b := buttons[len(buttons)-1]
	rows, detail := m.layout()
	if cmd := m.click(b.x+1, 7+rows+detail); cmd == nil || !m.refreshing || m.scanning {
		t.Fatal("restore did not refresh status")
	}
	drain(m)
	if len(m.visible()) != 1 || m.repos[0].Branch != "main" || len(m.targets(true)) != 1 {
		t.Fatal("restored repo is not usable")
	}
	reloaded, err := ignore.Load(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Contains(path) {
		t.Fatal("restore was not saved")
	}
}

func TestIgnoreWriteFailureAndBusyStateLeaveTargetsUnchanged(t *testing.T) {
	m := persistenceFixture(t)
	a := m.repos[0].Path
	root := t.TempDir()
	m.ignored, _ = ignore.Load(filepath.Join(root, "blocked", "ignored.json"))
	if err := os.WriteFile(filepath.Join(root, "blocked"), []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	m.selected[a] = true
	key(m, "i")
	if len(m.visible()) != 3 || !m.selected[a] || !strings.Contains(m.status, "save ignore file") {
		t.Fatal("save error lost selection or silently ignored repository")
	}
	m.ignored, _ = ignore.Load(filepath.Join(root, "valid.json"))
	m.running = true
	key(m, "i")
	if m.ignored.Contains(a) || len(m.targets(true)) != 3 {
		t.Fatal("ignore changed a running batch's scope")
	}
}

func TestRefreshSkipsDiscoveryAndScanFindsNewRepositories(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	known, added := filepath.Join(root, "known"), filepath.Join(root, "added")
	if err := os.MkdirAll(filepath.Join(known, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	m := New(context.Background(), []string{root}, testGit{}, nil)
	m.Init()
	drain(m)
	if len(m.repos) != 1 {
		t.Fatal("initial scan failed")
	}
	m.selected[known] = true
	m.repos[0].Branch = "stale"
	if err := os.MkdirAll(filepath.Join(added, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if cmd := key(m, "r"); cmd == nil || !m.refreshing || m.scanning {
		t.Fatal("r did not start status-only refresh")
	}
	if cmd := key(m, "s"); cmd != nil || m.scanning {
		t.Fatal("scan overlapped refresh")
	}
	if cmd := key(m, "F"); cmd != nil || m.running {
		t.Fatal("fetch overlapped refresh")
	}
	drain(m)
	if len(m.repos) != 1 || m.repos[0].Branch != "main" || !m.selected[known] {
		t.Fatal("refresh discovered folders or lost status/selection")
	}
	if cmd := key(m, "s"); cmd == nil || !m.scanning {
		t.Fatal("s did not start discovery")
	}
	drain(m)
	if len(m.repos) != 2 || !m.selected[known] {
		t.Fatal("scan did not discover added repository")
	}
}

func TestRefreshTargetsAllKnownNonIgnoredRepositories(t *testing.T) {
	m := persistenceFixture(t)
	a, b, c := m.repos[0].Path, m.repos[1].Path, m.repos[2].Path
	store, err := ignore.Load(filepath.Join(t.TempDir(), "ignored.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set([]string{a}, true); err != nil {
		t.Fatal(err)
	}
	m.ignored = store
	m.selected[b] = true
	m.search.SetValue("feature")
	key(m, "r")
	if m.refreshTotal != 2 {
		t.Fatal("refresh scope was affected by selection/filter or included ignored repo")
	}
	drain(m)
	if m.refreshDone != 2 || len(m.repos) != 3 || !m.selected[b] {
		t.Fatal("refresh lost repositories or selection")
	}
	if m.repos[1].Branch != "main" || m.repos[2].CommonDir != c {
		t.Fatal("known repositories were not updated")
	}
	if m.repos[0].CommonDir != "a" {
		t.Fatal("ignored repository was inspected")
	}
}

func TestRefreshWithoutKnownRepositoriesDoesNotScan(t *testing.T) {
	m := New(context.Background(), []string{t.TempDir()}, testGit{}, nil)
	if cmd := key(m, "r"); cmd != nil || m.busy() || !strings.Contains(m.status, "press s") {
		t.Fatal("empty refresh should suggest scanning")
	}
}

type refreshedStatusGit struct {
	testGit
	repo gitrepo.Repository
}

func (g refreshedStatusGit) Inspect(context.Context, string) gitrepo.Repository { return g.repo }

func TestRefreshClearsOldOperationState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		repo  gitrepo.Repository
		state string
	}{
		{name: "clean", repo: gitrepo.Repository{Upstream: "origin/main", TrackingKnown: true}, state: "clean"},
		{name: "changed", repo: gitrepo.Repository{Unstaged: 1}, state: "changed"},
		{name: "conflicts", repo: gitrepo.Repository{Conflicts: 1}, state: "conflicts"},
		{name: "inspection error", repo: gitrepo.Repository{Error: "working tree unavailable"}, state: "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := fixture()
			path := m.repos[0].Path
			tc.repo.Path = path
			m.git = refreshedStatusGit{repo: tc.repo}
			m.results[path] = result{action: gitrepo.Pull, state: "failed", err: "old pull failure", output: "old pull output"}
			if state := safe(m.rowState(m.repos[0])); state != "pull failed" {
				t.Fatalf("initial state: %s", state)
			}
			m.startRefresh([]string{path})
			drain(m)
			if state := safe(m.rowState(m.repos[0])); state != tc.state {
				t.Fatalf("refreshed state = %q; want %q", state, tc.state)
			}
			if _, ok := m.results[path]; ok {
				t.Fatal("old action result survived refresh")
			}
			if strings.Contains(m.output.GetContent(), "old pull") {
				t.Fatal("output still shows the cleared result")
			}
		})
	}
}

func TestPartialRefreshOnlyClearsResultsForRefreshedRepositories(t *testing.T) {
	m := fixture()
	for _, repo := range m.repos {
		m.results[repo.Path] = result{action: gitrepo.Pull, state: "failed", err: "old failure"}
	}
	m.refreshing, m.cancelling = true, true
	m.cancel = func() {}
	m.events = make(chan engine.Event)
	path := m.repos[0].Path
	m.handleEvent(eventMsg{open: true, event: engine.Event{Kind: "repository", Repository: m.repos[0]}})
	m.handleEvent(eventMsg{open: false})
	if _, ok := m.results[path]; ok {
		t.Fatal("refreshed repository retained its old result")
	}
	for _, repo := range m.repos[1:] {
		if m.results[repo.Path].state != "failed" {
			t.Fatal("unrefreshed repository lost its result")
		}
	}
}

type scanActionGit struct {
	testGit
	blockedPath string
	release     chan struct{}
	mu          sync.Mutex
	operated    map[string]bool
}

func (g *scanActionGit) Inspect(ctx context.Context, path string) gitrepo.Repository {
	if path == g.blockedPath {
		select {
		case <-g.release:
		case <-ctx.Done():
		}
	}
	g.mu.Lock()
	updated := g.operated[path]
	g.mu.Unlock()
	r := g.testGit.Inspect(ctx, path)
	if updated {
		r.Branch = "after-action"
	}
	return r
}

func (g *scanActionGit) Operate(_ context.Context, path string, action gitrepo.Action, output func(string)) (string, error) {
	g.mu.Lock()
	g.operated[path] = true
	g.mu.Unlock()
	text := string(action) + " completed"
	output(text)
	return text, nil
}

func TestGitActionsRunWhileStartupScanContinues(t *testing.T) {
	for _, action := range []struct {
		key    string
		action gitrepo.Action
	}{{"F", gitrepo.Fetch}, {"L", gitrepo.Pull}, {"P", gitrepo.Push}} {
		t.Run(string(action.action), func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
			for _, path := range []string{first, second} {
				if err := os.MkdirAll(filepath.Join(path, ".git"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			g := &scanActionGit{blockedPath: second, release: make(chan struct{}), operated: make(map[string]bool)}
			m := New(ctx, []string{root}, g, nil)
			m.Init()
			select {
			case event := <-m.events:
				m.Update(eventMsg{event: event, open: true})
			case <-ctx.Done():
				t.Fatal("first repository was not discovered")
			}
			if len(m.repos) != 1 || m.repos[0].Path != first || !m.scanning {
				t.Fatal("startup scan not in expected partial state")
			}
			if cmd := key(m, action.key); cmd == nil || !m.running || !m.scanning {
				t.Fatal("Git action was blocked by startup scan")
			}
			if m.total != 1 {
				t.Fatal("batch did not snapshot discovered repositories")
			}
			if cmd := key(m, "p"); cmd != nil {
				t.Fatal("second batch overlapped the first")
			}
			drainBatch(m)
			if !m.scanning || m.running {
				t.Fatal("batch completion stopped the scan")
			}
			if m.repos[0].Branch != "after-action" || m.results[first].action != action.action {
				t.Fatal("action did not finish while scan was blocked")
			}
			close(g.release)
			drain(m)
			if len(m.repos) != 2 || m.total != 1 || m.completed != 1 || m.results[first].state != "done" {
				t.Fatal("scan changed batch targets or cleared its result")
			}
			g.mu.Lock()
			count := len(g.operated)
			g.mu.Unlock()
			if count != 1 {
				t.Fatal("newly discovered repository was added to the running batch")
			}
		})
	}
}

func TestLateScanResultCannotOverwriteAction(t *testing.T) {
	m := fixture()
	m.scanning = true
	m.seen = make(map[string]bool)
	m.cancel = func() {}
	m.events = make(chan engine.Event)
	m.selected["/repos/a"] = true
	key(m, "l")
	stale := gitrepo.Repository{Path: "/repos/a", Branch: "stale", Error: "stale scan error"}
	m.Update(eventMsg{event: engine.Event{Kind: "repository", Repository: stale}, open: true})
	if m.results[stale.Path].state != "queued" || m.repos[0].Branch == "stale" {
		t.Fatal("scan overwrote queued action")
	}
	drainBatch(m)
	m.Update(eventMsg{event: engine.Event{Kind: "repository", Repository: stale}, open: true})
	if m.results[stale.Path].state != "done" || m.repos[0].Branch == "stale" || m.repos[0].Error != "" {
		t.Fatal("late scan snapshot overwrote completed action")
	}
	if !m.seen[stale.Path] {
		t.Fatal("scan did not record discovery of action target")
	}
	m.Update(eventMsg{open: false})
}

func TestScanCompletionDoesNotStopRunningBatch(t *testing.T) {
	m := fixture()
	scanCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.scanning, m.cancel = true, cancel
	m.seen = make(map[string]bool)
	m.events = make(chan engine.Event)
	key(m, "P")
	// Action targets must survive scan reconciliation, even if a rescan has
	// not reported them yet (for example, a folder disappeared meanwhile).
	_, cmd := m.Update(eventMsg{open: false})
	if cmd != nil || m.scanning || !m.running || scanCtx.Err() == nil {
		t.Fatal("scan completion changed the batch lifecycle")
	}
	if len(m.repos) != 3 || !strings.Contains(m.status, "push: 0/3") {
		t.Fatal("scan completion lost action targets or progress")
	}
	drainBatch(m)
	if m.completed != 3 || m.failed != 0 || m.busy() {
		t.Fatal("batch failed to finish after scan completion")
	}
}

func TestCancelAndQuitHandleBothScanAndBatch(t *testing.T) {
	for _, quit := range []bool{false, true} {
		for _, scanFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("quit=%v/scan-first=%v", quit, scanFirst), func(t *testing.T) {
				m := fixture()
				scanCtx, scanCancel := context.WithCancel(context.Background())
				defer scanCancel()
				batchCtx, batchCancel := context.WithCancel(context.Background())
				defer batchCancel()
				m.scanning, m.running = true, true
				m.cancel, m.batchCancel = scanCancel, batchCancel
				k := "x"
				if quit {
					k = "q"
				}
				if cmd := key(m, k); cmd != nil {
					t.Fatal("quit returned before active work stopped")
				}
				if scanCtx.Err() == nil || batchCtx.Err() == nil {
					t.Fatal("did not cancel both tasks")
				}
				var first, second tea.Msg = eventMsg{open: false}, batchMsg{eventMsg{open: false}}
				if !scanFirst {
					first, second = second, first
				}
				_, cmd := m.Update(first)
				if cmd != nil || !m.busy() {
					t.Fatal("first completion stopped the other task")
				}
				_, cmd = m.Update(second)
				if m.busy() {
					t.Fatal("work still marked active after both completions")
				}
				if quit {
					if cmd == nil {
						t.Fatal("quit did not finish")
					}
					if _, ok := cmd().(tea.QuitMsg); !ok {
						t.Fatal("expected terminal exit")
					}
				} else if cmd != nil {
					t.Fatal("cancel unexpectedly quit")
				}
			})
		}
	}
}
