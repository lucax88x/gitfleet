// Package ignore persists Gitfleet's ignored working-tree paths.
package ignore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type Store struct {
	path  string
	repos map[string]bool
}

type document struct {
	Repositories []string `json:"repositories"`
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gitfleet", "ignored.json"), nil
}

func Load(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	s := &Store{path: abs, repos: make(map[string]bool)}
	data, err := os.ReadFile(abs)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read ignore file %s: %w", abs, err)
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("read ignore file %s: %w", abs, err)
	}
	for _, path := range doc.Repositories {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("ignore file %s: repository path must be absolute: %q", abs, path)
		}
		s.repos[canonical(path)] = true
	}
	return s, nil
}

func canonical(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func (s *Store) Contains(path string) bool { return s != nil && s.repos[path] }

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) Snapshot() map[string]bool {
	repos := make(map[string]bool)
	if s != nil {
		for path := range s.repos {
			repos[path] = true
		}
	}
	return repos
}

// Set publishes in-memory changes only after the new file is safely written.
func (s *Store) Set(paths []string, ignored bool) error {
	if s == nil {
		return fmt.Errorf("ignore storage is unavailable")
	}
	next := s.Snapshot()
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("repository path must be absolute: %q", path)
		}
		path = canonical(path)
		if ignored {
			next[path] = true
		} else {
			delete(next, path)
		}
	}
	doc := document{Repositories: make([]string, 0, len(next))}
	for path := range next {
		doc.Repositories = append(doc.Repositories, path)
	}
	sort.Strings(doc.Repositories)
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("save ignore file %s: %w", s.path, err)
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".ignored-*.json")
	if err != nil {
		return fmt.Errorf("save ignore file %s: %w", s.path, err)
	}
	defer os.Remove(file.Name())
	_, err = file.Write(append(data, '\n'))
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), s.path)
	}
	if err != nil {
		return fmt.Errorf("save ignore file %s: %w", s.path, err)
	}
	s.repos = next
	return nil
}
