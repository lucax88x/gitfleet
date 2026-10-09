# Gitfleet

A Go terminal UI for seeing the state of your Git repositories and manually
fetching, pulling, or pushing a selection—or all of them.

```sh
mise install
just build
./gitfleet .
./gitfleet ~/repos
./gitfleet ~/work ~/personal
```

Requires Go 1.25+ to build and Git 2.31+ at runtime. Supports macOS, Linux,
and Windows.
Go and Just are configured in `mise.toml`. Activate mise in your shell, or use
`mise exec -- just` in place of `just`. Recipes use mise's Go toolchain.
Build dependencies are pinned in `go.mod` and `go.sum`. To install the executable
in your Go bin directory, run `just install` and ensure that directory is on PATH.

Gitfleet uses Bubble Tea v2, Bubbles, and Lip Gloss. It scans each supplied root
recursively, including the root itself, nested repositories, submodules, and
linked worktrees. Overlapping roots are deduplicated. Child directory symlinks,
Git internals, and bare repositories are skipped. Inaccessible paths appear as
scan warnings in the output panel. There is no saved workspace configuration.

## Downloads and automated releases

Every push to `main` runs formatting checks, `go vet`, race tests, and a native
build and help-command smoke test on Linux, macOS, and Windows. Pull requests
targeting `main` run the same checks without publishing a release.

After all checks pass, GitHub Actions publishes a release tagged
`main-<run-number>-<short-commit>` for the exact pushed commit. Releases contain
`gitfleet-linux-amd64.tar.gz`, `gitfleet-linux-arm64.tar.gz`,
`gitfleet-darwin-amd64.tar.gz`, `gitfleet-darwin-arm64.tar.gz`,
`gitfleet-windows-amd64.zip`, and `gitfleet-windows-arm64.zip`, plus a
`SHA256SUMS` checksum file. Each archive contains the `gitfleet` executable
(`gitfleet.exe` on Windows) and this README. Use `amd64` for Intel/AMD machines
or `arm64` for ARM machines,
including Apple Silicon; `darwin` means macOS. Download the matching archive
from the repository's GitHub Releases page and extract it to run `./gitfleet`
(or `.\gitfleet.exe` in PowerShell on Windows).
Git must be installed and available on PATH.

The workflow uses the built-in `GITHUB_TOKEN` with release write permission;
no additional secrets or manual tags are required. Rerunning a successful
workflow keeps its published release; a failed upload can resume from its draft.

## Reading the display

The table shows repository path, branch, file counts, ahead/behind counts, and
state. `S / M / ? / !` means staged, unstaged, untracked, and conflicted files.
A separate last-action column appears in terminals at least 100 columns wide.
A file with both staged and unstaged changes contributes to both counts.
`↑ / ↓` compares the current branch with its configured upstream. A dash means
the upstream is absent or unavailable; it does not mean the branch is synchronized.

Scanning only reads local state. **Ahead/behind counts reflect the last fetch.**
Fetch explicitly to update remote information. Detached HEAD, unborn branches,
missing upstreams, conflicts, and failures are shown separately. Select a row
to inspect its changed files and the output of its most recent operation.

During discovery, the status line reports how many repositories have been found
so far. You can fetch, pull, or push repositories as soon as they appear. Bulk
buttons say “Fetch/Pull/Push found” while scanning and target the non-ignored
repositories found so far. Once scanning finishes, they show the final totals.
Scrolling does not start another scan or change a running batch's targets.

## Controls

| Key / mouse | Action |
| --- | --- |
| `↑` / `↓`, `j` / `k` | Move between repositories |
| `g` / Home, `G` / End | Jump to first / last row |
| Space or click `[ ]` | Toggle selection |
| `a` | Toggle selection of all visible rows |
| Escape | Clear selection |
| `/` | Search path or branch; Enter/Escape leaves search |
| `[` / `]` or click a filter | All, Changed, Ahead/Behind, Ignored |
| `f` / `l` / `p` | Fetch / Pull / Push selected rows, or focused row if none selected |
| `F` / `L` / `P` | Fetch All / Pull All / Push All discovered repositories |
| Enter | Expand / collapse the output and files panel |
| Tab | Switch keyboard focus between list and output |
| Page Up / Page Down | Scroll output |
| Mouse wheel | Scroll the panel under the pointer |
| `s` | Scan folders for added/removed repositories and read their status |
| `r` | Refresh local status of all known non-ignored repositories; no folder scan |
| `i` / Ignore button | Ignore selected repositories, or the focused repository |
| `I` / Ignored tab | View ignored repositories; `i` / Restore includes them again |
| `x` | Cancel active work |
| `?` | Show help in the output panel |
| `q` / Ctrl+C | Cancel active work and exit |

The footer also has clickable Fetch, Pull, Push, Fetch All, Pull All, and Push All
buttons. **Actions start immediately.** Selected rows stay selected when hidden
by a filter; the footer explicitly counts hidden selections. “All” always means
all non-ignored discovered repositories, including hidden rows. A batch snapshots its targets
when started; changing selection or discovering additional repositories afterward
does not affect running work.

## Ignoring repositories

Select repositories and press `i` (or click Ignore) to hide them from the normal
list and exclude them from all Git operations, including Fetch All, Pull All, and
Push All. Press `I` to open the Ignored tab, then select repositories and press
`i` or click Restore to include them again. Restoration refreshes their status.
Entering or leaving the Ignored tab clears the selection.

Choices are saved immediately in a local JSON file and survive restarts:

- macOS: `~/Library/Application Support/gitfleet/ignored.json`
- Linux: `${XDG_CONFIG_HOME:-~/.config}/gitfleet/ignored.json`
- Windows: `%AppData%\gitfleet\ignored.json`

The help pane (`?`) shows the actual file path. Use
`gitfleet --ignore-file /path/to/ignored.json ~/repos` for a separate ignore list.
The file contains a `repositories` array of absolute paths, and is created on
the first change. Invalid files produce a startup error rather than discarding
your choices; failed saves leave the current list unchanged.

Ignores apply to exact working-tree paths. A nested repository or another linked
worktree can be ignored independently. Ignored repositories skip Git status
inspection, but discovery still traverses their folders to find nested repos.
The Ignored tab shows ignored repositories found under the current scan roots.
Ignoring is unavailable while a scan, refresh, or Git operation is running.

## Scan versus refresh

Startup and `s` walk the configured folder roots to discover repositories, remove
rows for repositories no longer present, and read local Git status. Folder
discovery is sequential; up to four status checks run in parallel as paths are
discovered.

`r` skips discovery and refreshes all known non-ignored repositories using the
same four-worker pool, regardless of selection or filters. It preserves the
repository list and selections. Each completed status check clears that
repository's previous operation result and output, so stale failures no longer
override its current state. This also applies to
status checks during `s`; cancelled refreshes retain results for unchecked repos.
Missing repositories display an error until a scan removes them. Restoring
ignored repositories refreshes only those paths.

Refresh usually finishes faster because it avoids walking the folder tree, but
Git status itself can be slow in large working trees. Neither operation contacts
remotes. Use Fetch to update remote tracking information. `x` cancels either
operation and retains results already received.

## Git operations

- Fetch executes `git fetch` with the repository's configured defaults.
- Pull executes `git pull`, honoring its configured merge/rebase and fast-forward
  settings. It may create a merge commit or leave conflicts that need resolving
  in the individual repository.
- Push executes `git push`, honoring configured destinations and refspecs. A push
  destination can differ from the upstream used for ahead/behind counts.

Gitfleet never adds force-push, reset, stash, or upstream-creation flags. Staging,
committing, branch switching, and conflict resolution belong in your existing
per-repository tools. There are no automatic fetches or scheduled operations.

Four independent Git directories run concurrently. Linked worktrees sharing Git
metadata run sequentially. One operation batch may run while a folder scan
continues. Only one operation batch runs at a time; explicit refresh requires
both scanning and operations to finish. `x` cancels both scanning and operations;
quitting waits for both to finish cancelling. Failures do not stop unrelated
repositories. Status refreshes after each action,
and its final output stays available until another action or an explicit
scan/refresh checks that repository again. A scan already in progress when an
action starts cannot overwrite that action's result with an older status snapshot.
Output retains the last 128 KiB per repository.

Git uses existing credential helpers and SSH agents. Terminal prompts and editors
are disabled; SSH runs in batch mode using the configured SSH command (which must
accept OpenSSH options). Authenticate in your normal terminal first if an action
reports missing credentials. Git hooks still run normally. Cancelling terminates
running Git process groups on macOS/Linux and leaves completed changes in place.

The minimum terminal size is 70 columns by 20 rows. The app restores the terminal
when it exits; `q` during a batch waits for cancellation and status refresh.

## Development

```sh
just                         # List commands
just setup                   # Install tools and download dependencies
just run ~/repos             # Run directly from source
just run ~/work ~/personal    # Scan multiple roots
just check                   # Formatting, vet, race tests, and build
just test -run TestFilters    # Run a particular test
```

The Justfile also provides `build`, `build-linux`, `install`, `deps`, `tidy`,
`fmt`, `fmt-check`, `vet`, `test`, `test-race`, `coverage`, and `clean`.
`build-linux` produces `gitfleet-linux-amd64`; `coverage` writes `coverage.out`.
Arguments are forwarded without shell interpolation, including paths with spaces.

Tests use temporary repositories and local bare remotes, without contacting remote
hosting services. The code separates Git discovery/status/commands, a concurrent
operation engine, and terminal state/rendering.
