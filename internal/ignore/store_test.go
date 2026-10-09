package ignore

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPersistenceAndRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "ignored.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("loading missing config created a file")
	}
	root := t.TempDir()
	repos := []string{filepath.Join(root, "with spaces"), filepath.Join(root, "other")}
	if err := s.Set(repos, true); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, repo := range repos {
		if !reloaded.Contains(repo) {
			t.Fatalf("missing persisted ignore: %s", repo)
		}
	}
	if reloaded.Contains(filepath.Join(repos[1], "nested")) {
		t.Fatal("ignore unexpectedly applied to a nested repository")
	}
	if err := reloaded.Set(repos[:1], false); err != nil {
		t.Fatal(err)
	}
	reloaded, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Contains(repos[0]) || !reloaded.Contains(repos[1]) {
		t.Fatal("restore did not persist independently")
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0600 {
		t.Fatalf("file permissions: %v", st.Mode())
	}
}

func TestSaveFailurePreservesMemoryAndFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "ignored.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(t.TempDir(), "kept")
	if err := s.Set([]string{kept}, true); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err := s.Set([]string{"relative/path"}, true); err == nil {
		t.Fatal("accepted relative path")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("invalid update changed file")
	}
	// Force an atomic-rename failure without relying on permission semantics.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.Set([]string{kept}, false); err == nil {
		t.Fatal("save unexpectedly succeeded")
	}
	if !s.Contains(kept) {
		t.Fatal("failed write changed in-memory state")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ignored-") {
			t.Fatal("temporary file leaked")
		}
	}
}

func TestMalformedFileFailsWithoutOverwriting(t *testing.T) {
	for _, contents := range []string{"not JSON", `{"repositories":["relative"]}`} {
		path := filepath.Join(t.TempDir(), "ignored.json")
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("invalid configuration accepted")
		}
		data, _ := os.ReadFile(path)
		if string(data) != contents {
			t.Fatal("invalid file overwritten")
		}
	}
}

func TestCanonicalPathsAndSnapshotIsolation(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	s, err := Load(filepath.Join(root, "ignored.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set([]string{alias, repo}, true); err != nil {
		t.Fatal(err)
	}
	snapshot := s.Snapshot()
	if len(snapshot) != 1 {
		t.Fatal("symlink alias was not deduplicated")
	}
	delete(snapshot, canonical(repo))
	if !s.Contains(canonical(repo)) {
		t.Fatal("scan snapshot mutated the store")
	}
}
