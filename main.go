package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"gitfleet/internal/gitrepo"
	"gitfleet/internal/ignore"
	"gitfleet/internal/tui"
)

func run(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("gitfleet", flag.ContinueOnError)
	flags.SetOutput(stderr)
	ignoreFile := flags.String("ignore-file", "", "override the persistent ignored-repositories file")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: gitfleet [paths...]\n\nScan Git working trees below one or more folders (default: .).\nSelect repositories in the TUI and manually fetch, pull, or push.\n\nExamples:\n  gitfleet .\n  gitfleet ~/repos\n  gitfleet ~/work ~/personal")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("Git is required: %w", err)
	}
	roots := flags.Args()
	if len(roots) == 0 {
		roots = []string{"."}
	}
	for i, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		roots[i] = abs
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if *ignoreFile == "" {
		path, err := ignore.DefaultPath()
		if err != nil {
			return fmt.Errorf("locate ignore file: %w", err)
		}
		*ignoreFile = path
	}
	ignored, err := ignore.Load(*ignoreFile)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(tui.New(ctx, roots, gitrepo.Git{}, ignored), tea.WithContext(ctx)).Run()
	return err
}

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "gitfleet:", err)
		os.Exit(1)
	}
}
