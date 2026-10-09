package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestDiscoverNestedWorktreesAndOverlappingRoots(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	nested := filepath.Join(parent, "nested")
	worktree := filepath.Join(root, "work tree")
	initRepo(t, parent)
	commit(t, parent, "file", "base")
	initRepo(t, nested)
	git(t, parent, "worktree", "add", "-b", "linked", worktree)
	if err := os.Symlink(parent, filepath.Join(root, "symlink")); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "--bare", filepath.Join(root, "bare.git"))
	// .git internals must not be discovered, even if they contain a marker.
	if err := os.MkdirAll(filepath.Join(parent, ".git", "fake", ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	var found []string
	Discover(context.Background(), []string{nested, root, parent}, func(p string) { found = append(found, p) }, func(err error) { t.Error(err) })
	sort.Strings(found)
	want := []string{parent, nested, worktree}
	sort.Strings(want)
	// macOS temporary paths may resolve through /var to /private/var.
	for i := range want {
		want[i], _ = filepath.EvalSymlinks(want[i])
	}
	if strings.Join(found, "\n") != strings.Join(want, "\n") {
		t.Fatalf("found %v; want %v", found, want)
	}
	if inspect(t, parent).CommonDir != inspect(t, worktree).CommonDir {
		t.Fatal("worktrees must share a scheduling key")
	}
}

func TestDiscoveryWarningsAndCancellation(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	warnings := 0
	Discover(context.Background(), []string{missing}, func(string) { t.Fatal("unexpected repository") }, func(error) { warnings++ })
	if warnings != 1 {
		t.Fatalf("warnings: %d", warnings)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	Discover(ctx, []string{root}, func(string) { t.Fatal("discovered after cancel") }, func(error) { t.Fatal("warning after cancel") })
}

func TestDiscoveryPermissionWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not use Unix directory permission bits")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can traverse mode-000 directories")
	}
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.Mkdir(blocked, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(blocked, 0700) })
	warnings := 0
	Discover(context.Background(), []string{root}, func(string) {}, func(error) { warnings++ })
	if warnings == 0 {
		t.Fatal("expected inaccessible directory warning")
	}
}

func TestParseStatusMalformedAndSpecialNames(t *testing.T) {
	data := "# branch.oid (initial)\x00# branch.head main\x00? file\nwith\ttabs\x00"
	r, err := ParseStatus([]byte(data))
	if err != nil || !r.Unborn || r.Files[0].Path != "file\nwith\ttabs" {
		t.Fatalf("parse: %+v %v", r, err)
	}
	for _, bad := range []string{"# branch.ab +oops -2\x00", "1 broken\x00", "2 R. N... 100644 100644 100644 a b R100 renamed\x00"} {
		if _, err := ParseStatus([]byte(bad)); err == nil {
			t.Fatalf("accepted malformed status %q", bad)
		}
	}
}
