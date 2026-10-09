# Repository Guidelines

## Project Structure & Module Organization

Gitfleet is a Go terminal UI for inspecting and operating on Git repositories.
`main.go` handles CLI arguments and startup. Packages under `internal/` separate
responsibilities:

- `gitrepo/`: discovery, status parsing, Git commands, and platform-specific process handling.
- `engine/`: concurrent scanning, refreshes, and operation batches.
- `ignore/`: persistent ignored-repository storage.
- `tui/`: Bubble Tea state, input handling, and rendering.

Tests live beside source files as `*_test.go`. `README.md` documents user behavior;
`.github/workflows/build-release.yml` defines verification and releases. There is
no separate asset directory.

## Build, Test, and Development Commands

Use Go 1.25+ and Git 2.31+. `mise.toml` configures Go and Just.

- `just setup`: install tools and download dependencies.
- `just run ~/repos`: launch the TUI against a directory.
- `just build`: build the local `gitfleet` executable.
- `just fmt`: format Go files with `gofmt`.
- `just check`: check formatting, run `go vet`, execute race tests, and build.
- `just test -run TestFilters`: run a focused test selection.
- `just coverage`: generate `coverage.out` and report function coverage.

If tools are unavailable on PATH, use `mise exec -- just <recipe>`.

## Coding Style & Naming Conventions

Follow standard Go conventions: use `gofmt` for tab indentation and formatting,
short lowercase package names, exported `PascalCase` identifiers, and unexported
`camelCase` identifiers. Keep Git execution in `gitrepo`, orchestration in
`engine`, and rendering in `tui`. Preserve context cancellation and platform
build constraints when changing process handling.

## Testing Guidelines

Use Go's standard `testing` package and `TestXxx` names. Git integration tests
must use `t.TempDir()`, isolated Git configuration, and local bare remotes;
avoid external hosting services. Add regression tests for changed behavior,
including cancellation or platform-specific cases when relevant. No numerical
coverage threshold is configured. Run `just check` before submitting; CI verifies
on Linux and cross-compiles release binaries for Linux, macOS, and Windows.

## Commit & Pull Request Guidelines

The repository has no commits yet, so no historical message convention exists.
Use concise imperative subjects, such as `Fix cancellation during refresh`.
Keep changes focused. PR descriptions should explain the problem, resulting
behavior, and validation commands; link relevant issues and include terminal
screenshots for visible UI changes. Update `README.md` when controls or behavior
change. Successful pushes to `main` automatically publish release binaries.

## Security & Configuration

Use existing Git credential helpers and SSH agents; never commit credentials.
Keep generated binaries, coverage output, and personal ignore lists out of Git.
