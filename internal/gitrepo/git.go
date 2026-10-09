package gitrepo

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Action string

const (
	Fetch Action = "fetch"
	Pull  Action = "pull"
	Push  Action = "push"
)

type Git struct{}

func command(ctx context.Context, path string, args ...string) *exec.Cmd {
	a := append([]string{"--no-optional-locks", "-C", path}, args...)
	cmd := exec.CommandContext(ctx, "git", a...)
	// Do not inherit a parent shell's repository overrides when scanning others.
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES":
			continue
		}
		cmd.Env = append(cmd.Env, e)
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=false", "LC_ALL=C")
	cmd.WaitDelay = 2 * time.Second
	configureProcess(cmd)
	return cmd
}

func (Git) Inspect(ctx context.Context, path string) Repository {
	r := Repository{Path: path}
	// A known repository may have been removed since discovery. Do not let
	// Git silently walk up to an enclosing repository and report its status.
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		r.Error = fmt.Sprintf("working tree unavailable: %v", err)
		return r
	}
	cmd := command(ctx, path, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		r, err = ParseStatus(out)
		r.Path = path
	}
	if err != nil {
		r.Error = strings.TrimSpace(stderr.String())
		if r.Error == "" {
			r.Error = err.Error()
		}
		return r
	}
	out, err = command(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		r.Error = fmt.Sprintf("resolve Git directory: %v", err)
		return r
	}
	r.CommonDir = strings.TrimSpace(string(out))
	return r
}

// Operate uses Git's configured remotes/refspecs and pull strategy. Output is
// bounded and streamed as snapshots so a noisy command cannot fill memory.
func (Git) Operate(ctx context.Context, path string, action Action, output func(string)) (string, error) {
	if action != Fetch && action != Pull && action != Push {
		return "", fmt.Errorf("unknown action %q", action)
	}
	cmd := command(ctx, path, "-c", "credential.interactive=false", string(action))
	ssh := os.Getenv("GIT_SSH_COMMAND")
	if ssh == "" {
		if configured, err := command(ctx, path, "config", "--get", "core.sshCommand").Output(); err == nil {
			ssh = strings.TrimSpace(string(configured))
		}
	}
	if ssh == "" {
		if binary := os.Getenv("GIT_SSH"); binary != "" {
			ssh = "'" + strings.ReplaceAll(binary, "'", "'\\''") + "'"
		} else {
			ssh = "ssh"
		}
	}
	cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND="+ssh+" -oBatchMode=yes", "SSH_ASKPASS_REQUIRE=never")
	buf := &tailWriter{notify: output}
	cmd.Stdout, cmd.Stderr = buf, buf
	err := cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return buf.String(), err
}

type tailWriter struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
	notify    func(string)
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	const limit = 128 * 1024
	n := len(p)
	w.data = append(w.data, p...)
	if len(w.data) > limit {
		w.data = append([]byte(nil), w.data[len(w.data)-limit:]...)
		w.truncated = true
	}
	if w.notify != nil {
		w.notify(w.text())
	}
	return n, nil
}

func (w *tailWriter) text() string {
	if w.truncated {
		return "[earlier output truncated]\n" + string(w.data)
	}
	return string(w.data)
}

func (w *tailWriter) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.text() }
