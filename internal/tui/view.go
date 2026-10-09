package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"gitfleet/internal/gitrepo"
)

var (
	accent     = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
	dim        = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	good       = lipgloss.NewStyle().Foreground(lipgloss.Color("78"))
	warn       = lipgloss.NewStyle().Foreground(lipgloss.Color("215"))
	bad        = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	focusStyle = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("255"))
	filters    = []string{"All", "Changed", "Ahead/Behind", "Ignored"}
)

// Never render control sequences originating in Git output, filenames, or refs.
func safe(s string) string {
	s = ansi.Strip(s)
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, s)
}

func safeOutput(s string) string {
	s = ansi.Strip(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return '�'
		}
		return r
	}, s)
}

func fit(s string, w int) string {
	return lipgloss.NewStyle().Width(w).Render(ansi.Truncate(s, w, "…"))
}

func (m *Model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "Gitfleet"
	if m.width < 70 || m.height < 20 {
		v.SetContent(fmt.Sprintf("Gitfleet needs at least 70 × 20 cells (currently %d × %d).\nResize the terminal. q quits; x cancels active work.", m.width, m.height))
		return v
	}
	var lines []string
	activity := ""
	if m.busy() {
		activity = "  " + m.spinner.View()
	}
	lines = append(lines, accent.Render(" GITFLEET")+dim.Render("  /  your repositories, at a glance")+activity)
	lines = append(lines, dim.Render(" Roots: "+safe(strings.Join(m.roots, " · "))))
	var tabs []string
	for i, f := range filters {
		s := " " + f + " "
		if i == m.filter {
			s = accent.Reverse(true).Render(s)
		} else {
			s = dim.Render(s)
		}
		tabs = append(tabs, s)
	}
	lines = append(lines, strings.Join(tabs, " "))
	if m.search.Focused() {
		lines = append(lines, m.search.View())
	} else {
		lines = append(lines, dim.Render(" / "+fallback(safe(m.search.Value()), "Search path or branch…")))
	}
	pathW, branchW := m.columnWidths()
	header := fit("    REPOSITORY", pathW) + " " + fit("BRANCH", branchW) + " " + fit("S / M / ? / !", 15) + " " + fit("↑ / ↓", 9) + " " + fit("STATE", 17)
	if m.width >= 100 {
		header += " LAST ACTION"
	}
	lines = append(lines, dim.Render(header))
	rows, _ := m.layout()
	visible := m.visible()
	for i := 0; i < rows; i++ {
		idx := m.offset + i
		if idx >= len(visible) {
			if i == 0 {
				lines = append(lines, dim.Render(" No repositories match. Try a different filter or scan root."))
			} else {
				lines = append(lines, "")
			}
			continue
		}
		r := visible[idx]
		mark := "[ ] "
		if m.selected[r.Path] {
			mark = "[x] "
		}
		branch := safe(r.Branch)
		if r.Detached {
			branch = "detached"
		} else if r.Unborn {
			branch += " (new)"
		}
		counts := fmt.Sprintf("%d / %d / %d / %d", r.Staged, r.Unstaged, r.Untracked, r.Conflicts)
		sync := "— / —"
		if r.TrackingKnown {
			sync = fmt.Sprintf("%d / %d", r.Ahead, r.Behind)
		}
		state := m.rowState(r)
		line := fit(mark+safe(m.displayPath(r.Path)), pathW) + " " + fit(branch, branchW) + " " + fit(counts, 15) + " " + fit(sync, 9) + " " + fit(state, 17)
		if m.width >= 100 {
			line += " " + m.lastAction(r.Path)
		}
		if idx == m.cursor {
			line = focusStyle.Width(m.width).Render(ansi.Truncate(line, m.width, "…"))
		}
		lines = append(lines, line)
	}
	title := " Output & files"
	if m.help {
		title = " Help"
	} else if m.outputFocus {
		title += " · focused"
	}
	lines = append(lines, accent.Render(title)+dim.Render("  · Enter expands · Tab switches focus · PgUp/PgDn scroll"))
	lines = append(lines, strings.Split(m.output.View(), "\n")...)
	hidden := 0
	vis := map[string]bool{}
	for _, r := range visible {
		vis[r.Path] = true
	}
	for path := range m.selected {
		if !vis[path] {
			hidden++
		}
	}
	ignoredCount := len(m.repos) - len(m.targets(true))
	summary := fmt.Sprintf(" %d/%d visible · %d selected (%d hidden) · action targets: %d", len(visible), len(m.repos), len(m.selected), hidden, len(m.targets(false)))
	if ignoredCount > 0 {
		summary += fmt.Sprintf(" · %d ignored", ignoredCount)
	}
	lines = append(lines, dim.Render(summary))
	buttons := m.buttons()
	for row := 0; row < 2; row++ {
		var parts []string
		for _, b := range buttons {
			if b.row == row {
				style := accent
				if m.actionsBlocked() || (b.ignore && m.scanning) {
					style = dim
				}
				parts = append(parts, style.Render(b.label))
			}
		}
		lines = append(lines, strings.Join(parts, " "))
	}
	status := m.status
	if m.scanning && (m.running || len(m.scanTouched) > 0) {
		status = fmt.Sprintf("Scanning: %d found · %s", len(m.seen), status)
	}
	lines = append(lines, fit(" "+safe(status), m.width))
	lines = append(lines, dim.Render(" ↑↓/jk move  Space select  a visible  Esc clear  [ ] filter  i ignore  I ignored  s scan  r refresh  x cancel  ? help  q quit"))
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, m.width, "…")
	}
	v.SetContent(strings.Join(lines, "\n"))
	return v
}

func (m *Model) columnWidths() (int, int) {
	branch := max(10, min(22, m.width/6))
	extra := 0
	if m.width >= 100 {
		extra = 17
	}
	return max(18, m.width-branch-15-9-17-4-extra), branch
}

func (m *Model) lastAction(path string) string {
	s, ok := m.results[path]
	if !ok {
		return dim.Render("—")
	}
	text := string(s.action) + " " + s.state
	switch s.state {
	case "done":
		return good.Render(text)
	case "failed":
		return bad.Render(text)
	default:
		return warn.Render(text)
	}
}

func (m *Model) rowState(r gitrepo.Repository) string {
	if m.ignored.Contains(r.Path) {
		return dim.Render("ignored")
	}
	if s, ok := m.results[r.Path]; ok {
		switch s.state {
		case "running", "queued":
			return warn.Render(string(s.action) + " " + s.state)
		case "failed":
			return bad.Render(string(s.action) + " failed")
		}
	}
	if r.Error != "" {
		return bad.Render("error")
	}
	if r.Conflicts > 0 {
		return bad.Render("conflicts")
	}
	if r.Detached {
		return warn.Render("detached HEAD")
	}
	if r.Unborn {
		return warn.Render("no commits")
	}
	if r.Ahead > 0 && r.Behind > 0 {
		return warn.Render("diverged")
	}
	if r.Changed() {
		return warn.Render("changed")
	}
	if r.Upstream == "" {
		return dim.Render("no upstream")
	}
	if !r.TrackingKnown {
		return warn.Render("upstream unknown")
	}
	if r.Ahead > 0 {
		return accent.Render("needs push")
	}
	if r.Behind > 0 {
		return accent.Render("needs pull")
	}
	return good.Render("clean")
}

type button struct {
	label  string
	row, x int
	action gitrepo.Action
	all    bool
	ignore bool
}

func (m *Model) buttons() []button {
	var buttons []button
	for row := 0; row < 2; row++ {
		x := 0
		for i, action := range []gitrepo.Action{gitrepo.Fetch, gitrepo.Pull, gitrepo.Push} {
			key := []string{"f", "l", "p"}[i]
			name := []string{"Fetch", "Pull", "Push"}[i]
			count := len(m.targets(false))
			if row == 1 {
				key = strings.ToUpper(key)
				name += " All"
				count = len(m.targets(true))
			}
			label := fmt.Sprintf("[%s %s %d]", key, name, count)
			if m.scanning && row == 1 {
				// A running scan has no final total. "Found" makes the partial
				// scope explicit, without a continually growing button counter.
				label = fmt.Sprintf("[%s %s found]", key, strings.TrimSuffix(name, " All"))
			}
			buttons = append(buttons, button{label: label, row: row, x: x, action: action, all: row == 1})
			x += lipgloss.Width(label) + 1
		}
	}
	name := "Ignore"
	if m.filter == ignoredFilter {
		name = "Restore"
	}
	label := fmt.Sprintf("[i %s %d]", name, len(m.ignoreTargets()))
	if m.scanning {
		label = fmt.Sprintf("[i %s …]", name)
	}
	last := buttons[2]
	buttons = append(buttons, button{label: label, row: 0, x: last.x + lipgloss.Width(last.label) + 1, ignore: true})
	return buttons
}

func (m *Model) click(x, y int) tea.Cmd {
	if m.width < 70 || m.height < 20 {
		return nil
	}
	if y == 2 {
		start := 0
		for i, f := range filters {
			width := len(f) + 2
			if x >= start && x < start+width {
				m.setFilter(i)
				return nil
			}
			start += width + 1
		}
	}
	if y == 3 {
		return m.search.Focus()
	}
	if m.search.Focused() {
		m.search.Blur()
	}
	rows, detail := m.layout()
	if y >= 5 && y < 5+rows {
		idx := m.offset + y - 5
		visible := m.visible()
		if idx < len(visible) {
			m.cursor = idx
			m.outputFocus = false
			if x < 4 {
				path := visible[idx].Path
				if m.selected[path] {
					delete(m.selected, path)
				} else {
					m.selected[path] = true
				}
			}
			m.refreshOutput(true)
		}
	}
	if y >= 6+rows && y < 6+rows+detail {
		m.outputFocus = true
	}
	for _, b := range m.buttons() {
		if y == 7+rows+detail+b.row && x >= b.x && x < b.x+lipgloss.Width(b.label) {
			if b.ignore {
				return m.toggleIgnore()
			}
			return m.startAction(b.action, b.all)
		}
	}
	return nil
}

const helpText = `GITFLEET — manual Git operations across working trees

Navigate       ↑/↓ or j/k; g/Home first; G/End last
Select         Space or click [ ]; a toggles all visible; Esc clears
Search         / then type; Enter/Esc returns to the list
Filter         [ and ] cycle tabs; click a tab directly
Inspect        Enter expands output/files; Tab focuses output
Scroll output  PgUp/PgDn; arrows when output is focused; mouse wheel

f / l / p      Fetch / Pull / Push selected repos, else focused repo
F / L / P      Fetch All / Pull All / Push All discovered repos
               All includes hidden rows, excluding ignored repositories.
               During scanning: operate on repos found so far. Newly found
               repos are not added to an already running batch.
               Actions start immediately. There is no confirmation.

i              Ignore selected/focused repos; in Ignored view, restore
I              Toggle Ignored view (clears selection)
               Ignore choices persist across app restarts.

s              Scan folders for added/removed repos and read their status
r              Refresh all known non-ignored repos' status (no folder scan)
               Status reads run in parallel with up to four workers.
               Neither scan nor refresh fetches from remotes.
x              Cancel active work; completed changes remain
q / Ctrl+C     Cancel active work and quit
? / Esc        Close help

S / M / ? / !  Staged / unstaged / untracked / conflicted file counts
↑ / ↓          Ahead / behind the configured upstream at last fetch
— / —          Upstream absent or unavailable; sync state is unknown

Pull uses each repository's Git configuration, including merge/rebase.
Resolve conflicts and create commits in your per-repository tools.
Push honors Git's configured destination and refspecs; these can differ
from the upstream used for ahead/behind counts.
Credentials must already be available via helpers or an SSH agent.
Interactive prompts and editors are disabled for background operations.
Four independent Git directories run concurrently; worktrees sharing
Git metadata run sequentially. One action batch can run alongside scanning.
x cancels both tasks; quitting waits for both to finish cancelling.
`
