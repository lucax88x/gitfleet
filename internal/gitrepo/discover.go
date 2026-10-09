package gitrepo

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Discover visits working trees once, including nested repositories. Bare
// repositories and symlinked child directories are not traversed.
func Discover(ctx context.Context, roots []string, found func(string), warning func(error)) {
	visited := make(map[string]bool)
	for _, root := range roots {
		if ctx.Err() != nil {
			return
		}
		abs, err := filepath.Abs(root)
		if err == nil {
			abs, err = filepath.EvalSymlinks(abs)
		}
		if err != nil {
			warning(fmt.Errorf("%s: %w", root, err))
			continue
		}
		info, err := os.Stat(abs)
		if err != nil {
			warning(fmt.Errorf("%s: %w", root, err))
			continue
		}
		if !info.IsDir() {
			warning(fmt.Errorf("%s: not a directory", root))
			continue
		}
		err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, walkErr error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if walkErr != nil {
				warning(fmt.Errorf("%s: %w", path, walkErr))
				return nil
			}
			// WalkDir already reads every directory's entries. Detect the Git
			// marker from those entries instead of probing .git, HEAD, and
			// config separately in every dependency/cache directory.
			if d.Name() == ".git" {
				if path != abs {
					if d.Type()&os.ModeSymlink == 0 {
						found(filepath.Dir(path))
					} else if _, err := os.Stat(path); err == nil {
						found(filepath.Dir(path))
					} else if !os.IsNotExist(err) {
						warning(fmt.Errorf("%s: %w", path, err))
					}
				}
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				// HEAD sorts before the object database. Skip the remaining
				// entries in a bare repository before descending into objects.
				if d.Name() == "HEAD" && regularFile(path) && regularFile(filepath.Join(filepath.Dir(path), "config")) {
					if st, err := os.Stat(filepath.Join(filepath.Dir(path), "objects")); err == nil && st.IsDir() {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if visited[path] {
				return filepath.SkipDir
			}
			visited[path] = true
			return nil
		})
		if err != nil && ctx.Err() == nil {
			warning(err)
		}
	}
}

func regularFile(path string) bool {
	s, err := os.Stat(path)
	return err == nil && s.Mode().IsRegular()
}
