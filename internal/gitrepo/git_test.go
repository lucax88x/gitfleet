package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Gitfleet Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "Gitfleet Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.invalid")
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_CONFIG_COUNT"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}

func git(t *testing.T, path string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", path}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func initRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	git(t, path, "init", "-b", "main")
}

func commit(t *testing.T, path, name, content string) {
	t.Helper()
	write(t, path, name, content)
	git(t, path, "add", "--", name)
	git(t, path, "commit", "-m", name)
}

func inspect(t *testing.T, path string) Repository {
	t.Helper()
	r := (Git{}).Inspect(context.Background(), path)
	if r.Error != "" {
		t.Fatal(r.Error)
	}
	return r
}

func TestInspectWorkingTreeStates(t *testing.T) {
	isolate(t)
	p := filepath.Join(t.TempDir(), "repo with spaces")
	initRepo(t, p)
	r := inspect(t, p)
	if !r.Unborn || r.Branch != "main" || r.Upstream != "" {
		t.Fatalf("unborn: %+v", r)
	}
	commit(t, p, "tracked", "original\n")
	if inspect(t, p).Changed() {
		t.Fatal("new commit should be clean")
	}
	renamed, untracked := "renamed\nwith spaces", "untracked\nfile"
	if runtime.GOOS == "windows" {
		// Windows filenames cannot contain newline characters.
		renamed, untracked = "renamed with spaces", "untracked file"
	}
	git(t, p, "mv", "tracked", renamed)
	write(t, p, renamed, "edited\n")
	write(t, p, untracked, "new")
	r = inspect(t, p)
	if r.Staged != 1 || r.Unstaged != 1 || r.Untracked != 1 || len(r.Files) != 2 {
		t.Fatalf("counts: %+v", r)
	}
	if r.Files[0].Path != renamed {
		t.Fatalf("rename path: %q", r.Files[0].Path)
	}
	git(t, p, "checkout", "--detach")
	if !inspect(t, p).Detached {
		t.Fatal("missing detached HEAD")
	}
}

func TestInspectConflict(t *testing.T) {
	isolate(t)
	p := t.TempDir()
	initRepo(t, p)
	commit(t, p, "file", "base\n")
	git(t, p, "checkout", "-b", "other")
	commit(t, p, "file", "other\n")
	git(t, p, "checkout", "main")
	commit(t, p, "file", "main\n")
	if err := exec.Command("git", "-C", p, "merge", "other").Run(); err == nil {
		t.Fatal("expected conflict")
	}
	if r := inspect(t, p); r.Conflicts != 1 || r.Files[0].Status != "UU" {
		t.Fatalf("conflict: %+v", r)
	}
}

func pairedRepos(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	git(t, root, "init", "--bare", "--initial-branch=main", remote)
	git(t, root, "clone", remote, a)
	commit(t, a, "base", "base\n")
	git(t, a, "push", "-u", "origin", "main")
	git(t, root, "clone", remote, b)
	return a, b, remote
}

func operate(t *testing.T, path string, action Action) {
	t.Helper()
	out, err := (Git{}).Operate(context.Background(), path, action, nil)
	if err != nil {
		t.Fatalf("%s: %v\n%s", action, err, out)
	}
}

func TestFetchPullPushAndRejection(t *testing.T) {
	isolate(t)
	a, b, remote := pairedRepos(t)
	commit(t, a, "local", "local\n")
	if r := inspect(t, a); r.Ahead != 1 || r.Behind != 0 {
		t.Fatalf("ahead: %+v", r)
	}
	operate(t, a, Push)
	if inspect(t, a).Ahead != 0 {
		t.Fatal("push did not update tracking ref")
	}
	if inspect(t, b).Behind != 0 {
		t.Fatal("inspection unexpectedly fetched")
	}
	operate(t, b, Fetch)
	if inspect(t, b).Behind != 1 {
		t.Fatal("fetch did not expose incoming commit")
	}
	git(t, b, "config", "pull.ff", "only")
	operate(t, b, Pull)
	if inspect(t, b).Behind != 0 {
		t.Fatal("pull did not update branch")
	}
	commit(t, a, "a2", "a2")
	commit(t, b, "b2", "b2")
	operate(t, b, Push)
	before := git(t, remote, "rev-parse", "main")
	out, err := (Git{}).Operate(context.Background(), a, Push, nil)
	if err == nil || !strings.Contains(out, "rejected") {
		t.Fatalf("expected push rejection: %v %s", err, out)
	}
	if after := git(t, remote, "rev-parse", "main"); before != after {
		t.Fatal("rejected push changed remote")
	}
	operate(t, a, Fetch)
	if r := inspect(t, a); r.Ahead != 1 || r.Behind != 1 {
		t.Fatalf("divergence: %+v", r)
	}
}

func TestPullRespectsConfiguration(t *testing.T) {
	for _, mode := range []string{"merge", "rebase", "ff-only"} {
		t.Run(mode, func(t *testing.T) {
			isolate(t)
			a, b, remote := pairedRepos(t)
			commit(t, a, "local", "local")
			commit(t, b, "remote", "remote")
			operate(t, b, Push)
			switch mode {
			case "merge":
				git(t, a, "config", "pull.rebase", "false")
			case "rebase":
				git(t, a, "config", "pull.rebase", "true")
			case "ff-only":
				git(t, a, "config", "pull.ff", "only")
			}
			out, err := (Git{}).Operate(context.Background(), a, Pull, nil)
			if mode == "ff-only" {
				if err == nil {
					t.Fatal("expected divergent fast-forward rejection")
				}
				return
			}
			if err != nil {
				t.Fatalf("pull %s: %v %s", mode, err, out)
			}
			parents := strings.Fields(git(t, a, "rev-list", "--parents", "-n", "1", "HEAD"))
			if mode == "merge" && len(parents) != 3 {
				t.Fatalf("wanted merge commit: %v", parents)
			}
			if mode == "rebase" && (len(parents) != 2 || parents[1] != git(t, remote, "rev-parse", "main")) {
				t.Fatalf("wanted rebased commit: %v", parents)
			}
		})
	}
}

func TestInspectIgnoresInheritedRepositoryOverrides(t *testing.T) {
	isolate(t)
	a, b, _ := pairedRepos(t)
	write(t, b, "untracked", "new")
	t.Setenv("GIT_DIR", filepath.Join(a, ".git"))
	t.Setenv("GIT_WORK_TREE", a)
	if r := inspect(t, b); r.Untracked != 1 {
		t.Fatalf("used wrong worktree: %+v", r)
	}
}

func TestMissingUpstreamIsNotReportedAsSynchronized(t *testing.T) {
	isolate(t)
	a, _, _ := pairedRepos(t)
	if !inspect(t, a).TrackingKnown {
		t.Fatal("existing upstream should have known counts")
	}
	git(t, a, "update-ref", "-d", "refs/remotes/origin/main")
	r := inspect(t, a)
	if r.Upstream != "origin/main" || r.TrackingKnown {
		t.Fatalf("missing upstream: %+v", r)
	}
}

func TestInspectRemovedRepositoryDoesNotFallBackToParent(t *testing.T) {
	isolate(t)
	parent := t.TempDir()
	initRepo(t, parent)
	commit(t, parent, "file", "parent")
	nested := filepath.Join(parent, "nested")
	initRepo(t, nested)
	if err := os.RemoveAll(filepath.Join(nested, ".git")); err != nil {
		t.Fatal(err)
	}
	r := (Git{}).Inspect(context.Background(), nested)
	if r.Path != nested || r.Error == "" || r.Branch != "" {
		t.Fatalf("reported parent status for removed repository: %+v", r)
	}
}

func TestCommandCancellation(t *testing.T) {
	isolate(t)
	a, _, _ := pairedRepos(t)
	hook := filepath.Join(a, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho hook-started\nsleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := (Git{}).Operate(ctx, a, Push, func(s string) {
			if strings.Contains(s, "hook-started") {
				cancel()
			}
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("command did not cancel promptly")
	}
}

func TestTailWriterBounded(t *testing.T) {
	w := &tailWriter{}
	w.Write([]byte(strings.Repeat("a", 200*1024)))
	w.Write([]byte("last line"))
	s := w.String()
	if len(s) > 129*1024 || !strings.HasPrefix(s, "[earlier output truncated]") || !strings.HasSuffix(s, "last line") {
		t.Fatal("bad output tail")
	}
}
