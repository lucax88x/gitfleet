package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"gitfleet/internal/engine"
	"gitfleet/internal/gitrepo"
	"gitfleet/internal/ignore"
)

type result struct {
	action             gitrepo.Action
	state, output, err string
	finished           time.Time
}

type eventMsg struct {
	event engine.Event
	open  bool
}

type batchMsg struct{ eventMsg }

type Model struct {
	ctx                                      context.Context
	git                                      engine.Client
	ignored                                  *ignore.Store
	roots                                    []string
	repos                                    []gitrepo.Repository
	selected                                 map[string]bool
	results                                  map[string]result
	cursor, offset, width, height            int
	filter                                   int
	search                                   textinput.Model
	output                                   viewport.Model
	spinner                                  spinner.Model
	details, outputFocus, help               bool
	scanning, running, quitting, cancelling  bool
	refreshing                               bool
	refreshDone, refreshTotal, refreshFailed int
	cancel                                   context.CancelFunc
	events                                   <-chan engine.Event
	batchEvents                              <-chan engine.Event
	batchCancel                              context.CancelFunc
	batchCancelling                          bool
	scanTouched                              map[string]bool
	seen                                     map[string]bool
	warnings                                 []string
	status                                   string
	batchAction                              gitrepo.Action
	completed, total, failed                 int
}

func New(ctx context.Context, roots []string, git engine.Client, ignored *ignore.Store) *Model {
	input := textinput.New()
	input.Prompt = "/ "
	input.Placeholder = "Search repositories or branches"
	input.CharLimit = 200
	s := spinner.New()
	s.Spinner = spinner.Dot
	v := viewport.New()
	v.SoftWrap = true
	m := &Model{ctx: ctx, git: git, ignored: ignored, roots: roots, selected: map[string]bool{}, results: map[string]result{},
		search: input, output: v, spinner: s, width: 100, height: 30, status: "Reading local Git state; fetch explicitly to update remote information."}
	m.refreshOutput(true)
	return m
}

func (m *Model) Init() tea.Cmd { return m.startScan() }

func waitEvent(events <-chan engine.Event) tea.Cmd {
	return func() tea.Msg { e, ok := <-events; return eventMsg{e, ok} }
}

func waitBatchEvent(events <-chan engine.Event) tea.Cmd {
	return func() tea.Msg { e, ok := <-events; return batchMsg{eventMsg{e, ok}} }
}

func (m *Model) startScan() tea.Cmd {
	if m.busy() {
		m.status = "Wait for the current task, or press x to cancel."
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel, m.scanning, m.cancelling = cancel, true, false
	m.seen, m.warnings = map[string]bool{}, nil
	m.scanTouched = make(map[string]bool)
	m.events = engine.Scan(ctx, m.roots, m.git, m.ignored.Snapshot())
	m.status = "Scanning folders and reading local Git state…"
	return tea.Batch(waitEvent(m.events), m.spinner.Tick)
}

func (m *Model) startRefresh(paths []string) tea.Cmd {
	if m.busy() {
		m.status = "Wait for the current task, or press x to cancel."
		return nil
	}
	if len(paths) == 0 {
		m.status = "No repositories to refresh; press s to scan folders."
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel, m.refreshing, m.cancelling = cancel, true, false
	m.refreshDone, m.refreshTotal, m.refreshFailed = 0, len(paths), 0
	m.events = engine.Refresh(ctx, paths, m.git)
	m.status = fmt.Sprintf("Refreshing local status: 0/%d · x cancels", m.refreshTotal)
	return tea.Batch(waitEvent(m.events), m.spinner.Tick)
}

func (m *Model) busy() bool { return m.scanning || m.refreshing || m.running }

func (m *Model) actionsBlocked() bool {
	return m.running || m.refreshing || m.cancelling || m.quitting
}

func (m *Model) visible() []gitrepo.Repository {
	q := strings.ToLower(m.search.Value())
	var rows []gitrepo.Repository
	for _, r := range m.repos {
		if m.ignored.Contains(r.Path) != (m.filter == ignoredFilter) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(r.Path+" "+r.Branch), q) {
			continue
		}
		match := true
		switch m.filter {
		case changedFilter:
			match = r.Changed()
		case syncFilter:
			match = r.Ahead > 0 || r.Behind > 0
		}
		if match {
			rows = append(rows, r)
		}
	}
	return rows
}

func (m *Model) focused() (gitrepo.Repository, bool) {
	rows := m.visible()
	if len(rows) == 0 {
		return gitrepo.Repository{}, false
	}
	return rows[min(max(m.cursor, 0), len(rows)-1)], true
}

func (m *Model) targets(all bool) []gitrepo.Repository {
	var repos []gitrepo.Repository
	for _, r := range m.repos {
		if !m.ignored.Contains(r.Path) && (all || m.selected[r.Path]) {
			repos = append(repos, r)
		}
	}
	if !all && len(repos) == 0 {
		if r, ok := m.focused(); ok && !m.ignored.Contains(r.Path) {
			repos = append(repos, r)
		}
	}
	return repos
}

const (
	allFilter = iota
	changedFilter
	syncFilter
	ignoredFilter
)

func (m *Model) setFilter(filter int) {
	if (m.filter == ignoredFilter) != (filter == ignoredFilter) {
		m.selected = make(map[string]bool)
	}
	m.filter, m.cursor, m.offset = filter, 0, 0
	m.refreshOutput(true)
}

func (m *Model) ignoreTargets() []string {
	var paths []string
	for _, r := range m.repos {
		if m.selected[r.Path] && m.ignored.Contains(r.Path) == (m.filter == ignoredFilter) {
			paths = append(paths, r.Path)
		}
	}
	if len(paths) == 0 {
		if r, ok := m.focused(); ok {
			paths = append(paths, r.Path)
		}
	}
	return paths
}

func (m *Model) toggleIgnore() tea.Cmd {
	if m.busy() {
		m.status = "Wait for the current task, or press x to cancel."
		return nil
	}
	paths := m.ignoreTargets()
	if len(paths) == 0 {
		m.status = "No repositories to ignore or restore."
		return nil
	}
	ignoring := m.filter != ignoredFilter
	if err := m.ignored.Set(paths, ignoring); err != nil {
		m.status = err.Error()
		return nil
	}
	for _, path := range paths {
		delete(m.selected, path)
	}
	m.clamp()
	m.refreshOutput(true)
	if !ignoring {
		// Ignored repositories skip Git inspection, so restore their status
		// before allowing operations on them again.
		for i := range m.repos {
			if !m.ignored.Contains(m.repos[i].Path) && m.repos[i].CommonDir == "" {
				m.repos[i].Error = "Status needs refreshing; press r to refresh."
			}
		}
		m.setFilter(0)
		return m.startRefresh(paths)
	}
	m.status = fmt.Sprintf("Ignored %d repositories · saved to %s · I shows ignored repositories", len(paths), m.ignored.Path())
	return nil
}

func (m *Model) startAction(action gitrepo.Action, all bool) tea.Cmd {
	if m.actionsBlocked() {
		m.status = "Wait for the current task, or press x to cancel."
		return nil
	}
	repos := m.targets(all)
	if len(repos) == 0 {
		m.status = "No repositories to operate on."
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.batchCancel, m.running, m.batchCancelling = cancel, true, false
	m.batchAction, m.total, m.completed, m.failed = action, len(repos), 0, 0
	for _, r := range repos {
		m.results[r.Path] = result{action: action, state: "queued"}
		if m.scanning {
			if m.scanTouched == nil {
				m.scanTouched = make(map[string]bool)
			}
			m.scanTouched[r.Path] = true
		}
	}
	m.batchEvents = engine.Batch(ctx, repos, action, m.git)
	m.status = fmt.Sprintf("%s: 0/%d completed · x cancels", action, m.total)
	m.refreshOutput(false)
	if m.scanning {
		return waitBatchEvent(m.batchEvents)
	}
	return tea.Batch(waitBatchEvent(m.batchEvents), m.spinner.Tick)
}

func (m *Model) putRepository(r gitrepo.Repository) {
	focused, ok := m.focused()
	updated := false
	for i := range m.repos {
		if m.repos[i].Path == r.Path {
			m.repos[i] = r
			updated = true
			break
		}
	}
	if !updated {
		m.repos = append(m.repos, r)
		sort.Slice(m.repos, func(i, j int) bool { return m.repos[i].Path < m.repos[j].Path })
	}
	if ok {
		for i, row := range m.visible() {
			if row.Path == focused.Path {
				m.cursor = i
				break
			}
		}
	}
	m.clamp()
}

func (m *Model) handleEvent(msg eventMsg) tea.Cmd {
	if !msg.open {
		m.cancel()
		var status string
		if m.scanning {
			if !m.cancelling {
				kept := m.repos[:0]
				for _, r := range m.repos {
					if m.seen[r.Path] || m.scanTouched[r.Path] {
						kept = append(kept, r)
					} else {
						delete(m.selected, r.Path)
						delete(m.results, r.Path)
					}
				}
				m.repos = kept
			}
			status = fmt.Sprintf("%d repositories · %d scan warnings · remote counts reflect the last fetch", len(m.repos), len(m.warnings))
		} else if m.refreshing {
			status = fmt.Sprintf("Status refreshed: %d/%d repositories · %d errors · remote counts reflect the last fetch", m.refreshDone, m.refreshTotal, m.refreshFailed)
		}
		if m.cancelling {
			status = "Cancelled. Completed Git changes are retained. " + status
		}
		if !m.running {
			m.status = status
		}
		m.scanning, m.cancelling = false, false
		m.refreshing = false
		m.clamp()
		m.refreshOutput(false)
		if m.quitting && !m.busy() {
			return tea.Quit
		}
		return nil
	}
	e := msg.event
	switch e.Kind {
	case "repository":
		if m.scanning {
			m.seen[e.Repository.Path] = true
		}
		// A scan may have inspected this row before an action started. Its
		// snapshot must not replace the action's newer state or output.
		if m.scanning && m.scanTouched[e.Repository.Path] {
			break
		}
		// Explicit status reads supersede old operation results. Clear only
		// inspected repositories so cancelled/partial refreshes keep the rest.
		if (m.scanning || m.refreshing) && !m.ignored.Contains(e.Repository.Path) {
			delete(m.results, e.Repository.Path)
		}
		m.putRepository(e.Repository)
		if m.scanning && !m.cancelling && !m.running && len(m.scanTouched) == 0 {
			m.status = fmt.Sprintf("Scanning… %d repositories found so far · Git actions available on listed repos · x cancels", len(m.seen))
		}
		if m.refreshing {
			m.refreshDone++
			if e.Repository.Error != "" {
				m.refreshFailed++
			}
			if !m.cancelling {
				m.status = fmt.Sprintf("Refreshing local status: %d/%d · %d errors · x cancels", m.refreshDone, m.refreshTotal, m.refreshFailed)
			}
		}
	case "warning":
		m.warnings = append(m.warnings, e.Err.Error())
	}
	m.refreshOutput(false)
	return waitEvent(m.events)
}

func (m *Model) handleBatchEvent(msg eventMsg) tea.Cmd {
	if !msg.open {
		m.batchCancel()
		m.status = fmt.Sprintf("%s finished: %d/%d completed · %d failed or cancelled", m.batchAction, m.completed, m.total, m.failed)
		if m.batchCancelling {
			m.status = "Cancelled. Completed Git changes are retained. " + m.status
		}
		m.running, m.batchCancelling = false, false
		m.refreshOutput(false)
		if m.quitting && !m.busy() {
			return tea.Quit
		}
		return nil
	}
	e := msg.event
	switch e.Kind {
	case "started":
		r := m.results[e.Path]
		r.state = "running"
		m.results[e.Path] = r
	case "output":
		r := m.results[e.Path]
		r.output = e.Output
		m.results[e.Path] = r
	case "finished":
		r := m.results[e.Path]
		r.state, r.output, r.finished = "done", e.Output, time.Now()
		if e.Err != nil {
			r.state, r.err = "failed", e.Err.Error()
			m.failed++
		}
		m.results[e.Path] = r
		m.putRepository(e.Repository)
		m.completed++
		m.status = fmt.Sprintf("%s: %d/%d completed · %d failed · x cancels", m.batchAction, m.completed, m.total, m.failed)
	}
	m.refreshOutput(false)
	return waitBatchEvent(m.batchEvents)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.search.SetWidth(max(10, m.width-4))
		m.clamp()
		m.refreshOutput(false)
	case eventMsg:
		return m, m.handleEvent(msg)
	case batchMsg:
		return m, m.handleBatchEvent(msg.eventMsg)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.busy() {
			return m, cmd
		}
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, m.quit()
		}
		if m.search.Focused() {
			if key == "enter" || key == "esc" {
				m.search.Blur()
				return m, nil
			}
			var cmd tea.Cmd
			m.search, cmd = m.search.Update(msg)
			m.cursor, m.offset = 0, 0
			m.refreshOutput(true)
			return m, cmd
		}
		if m.help {
			if key == "?" || key == "esc" || key == "q" {
				m.help = false
				m.refreshOutput(true)
				return m, nil
			}
			var cmd tea.Cmd
			m.output, cmd = m.output.Update(msg)
			return m, cmd
		}
		switch key {
		case "q":
			return m, m.quit()
		case "x":
			if m.busy() {
				m.cancelWork()
				m.status = "Cancelling active work…"
			}
		case "f":
			return m, m.startAction(gitrepo.Fetch, false)
		case "l":
			return m, m.startAction(gitrepo.Pull, false)
		case "p":
			return m, m.startAction(gitrepo.Push, false)
		case "F":
			return m, m.startAction(gitrepo.Fetch, true)
		case "L":
			return m, m.startAction(gitrepo.Pull, true)
		case "P":
			return m, m.startAction(gitrepo.Push, true)
		case "r":
			var paths []string
			for _, repo := range m.targets(true) {
				paths = append(paths, repo.Path)
			}
			return m, m.startRefresh(paths)
		case "s":
			return m, m.startScan()
		case "i":
			return m, m.toggleIgnore()
		case "I":
			if m.filter == ignoredFilter {
				m.setFilter(0)
			} else {
				m.setFilter(ignoredFilter)
			}
		case "/":
			return m, m.search.Focus()
		case "?":
			m.help = true
			m.refreshOutput(true)
		case "tab":
			m.outputFocus = !m.outputFocus
		case "enter":
			m.details = !m.details
			m.clamp()
			m.refreshOutput(true)
		case "esc":
			m.selected = map[string]bool{}
			m.outputFocus = false
		case " ", "space":
			if r, ok := m.focused(); ok {
				if m.selected[r.Path] {
					delete(m.selected, r.Path)
				} else {
					m.selected[r.Path] = true
				}
			}
		case "a":
			rows := m.visible()
			every := len(rows) > 0
			for _, r := range rows {
				if !m.selected[r.Path] {
					every = false
					break
				}
			}
			for _, r := range rows {
				if every {
					delete(m.selected, r.Path)
				} else {
					m.selected[r.Path] = true
				}
			}
		case "[", "]":
			if key == "]" {
				m.setFilter((m.filter + 1) % len(filters))
			} else {
				m.setFilter((m.filter + len(filters) - 1) % len(filters))
			}
		default:
			if m.outputFocus || key == "pgup" || key == "pgdown" {
				var cmd tea.Cmd
				m.output, cmd = m.output.Update(msg)
				return m, cmd
			}
			switch key {
			case "j", "down":
				m.cursor++
			case "k", "up":
				m.cursor--
			case "home", "g":
				m.cursor = 0
			case "end", "G":
				m.cursor = len(m.visible()) - 1
			}
			m.clamp()
			m.refreshOutput(true)
		}
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			return m, m.click(msg.X, msg.Y)
		}
	case tea.MouseWheelMsg:
		rows, _ := m.layout()
		if msg.Y >= 6+rows {
			var cmd tea.Cmd
			m.output, cmd = m.output.Update(msg)
			return m, cmd
		}
		if msg.Button == tea.MouseWheelUp {
			m.cursor -= 3
		} else if msg.Button == tea.MouseWheelDown {
			m.cursor += 3
		}
		m.clamp()
		m.refreshOutput(true)
	default:
		if m.search.Focused() {
			var cmd tea.Cmd
			m.search, cmd = m.search.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m *Model) quit() tea.Cmd {
	if m.busy() {
		m.quitting = true
		m.cancelWork()
		m.status = "Cancelling active work before exit…"
		return nil
	}
	return tea.Quit
}

func (m *Model) cancelWork() {
	if m.scanning || m.refreshing {
		m.cancelling = true
		m.cancel()
	}
	if m.running {
		m.batchCancelling = true
		m.batchCancel()
	}
}

func (m *Model) layout() (int, int) {
	detail := max(4, m.height/4)
	if m.details {
		detail = max(4, m.height/2)
	}
	detail = min(detail, max(4, m.height-14))
	return max(3, m.height-detail-11), detail
}

func (m *Model) clamp() {
	n := len(m.visible())
	m.cursor = max(0, min(m.cursor, n-1))
	rows, _ := m.layout()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	m.offset = max(0, min(m.offset, max(0, n-rows)))
}

func (m *Model) displayPath(path string) string {
	if len(m.roots) == 1 {
		if rel, err := filepath.Rel(m.roots[0], path); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return rel
		}
	}
	return path
}

func (m *Model) refreshOutput(reset bool) {
	_, h := m.layout()
	m.output.SetWidth(max(1, m.width))
	m.output.SetHeight(h)
	var b strings.Builder
	if m.help {
		b.WriteString(helpText)
		fmt.Fprintf(&b, "\nIgnore file: %s\n", safe(m.ignored.Path()))
	} else if r, ok := m.focused(); ok {
		if m.ignored.Contains(r.Path) {
			fmt.Fprintf(&b, "%s\nIgnored — excluded from Git operations, including All.\nPress i or click Restore to include this repository again.\nSaved in: %s\n", safe(r.Path), safe(m.ignored.Path()))
		} else {
			fmt.Fprintf(&b, "%s\nBranch: %s   Upstream: %s\n", safe(r.Path), safe(r.Branch), fallback(safe(r.Upstream), "none"))
			if r.Error != "" {
				fmt.Fprintf(&b, "Status error: %s\n", safe(r.Error))
			}
			if result, ok := m.results[r.Path]; ok {
				fmt.Fprintf(&b, "$ git %s — %s", result.action, result.state)
				if !result.finished.IsZero() {
					fmt.Fprintf(&b, " at %s", result.finished.Format("15:04:05"))
				}
				b.WriteString("\n")
				if result.output != "" {
					b.WriteString(safeOutput(result.output))
					b.WriteString("\n")
				}
				if result.err != "" {
					fmt.Fprintf(&b, "Error: %s\n", safe(result.err))
				}
			} else {
				b.WriteString("No action run. Remote counts reflect the last fetch.\n")
			}
			if len(r.Files) > 0 {
				b.WriteString("\nWorking tree files:\n")
				for _, f := range r.Files {
					fmt.Fprintf(&b, "  %s  %s\n", f.Status, safe(f.Path))
				}
			}
		}
	} else {
		b.WriteString("No matching repositories. Use / to search, [ ] to change filter, or s to scan folders.\n")
	}
	if !m.help && len(m.warnings) > 0 {
		b.WriteString("\nScan warnings:\n")
		for _, w := range m.warnings {
			b.WriteString(safe(w) + "\n")
		}
	}
	m.output.SetContent(b.String())
	if reset {
		m.output.GotoTop()
	}
}

func fallback(s, other string) string {
	if s == "" {
		return other
	}
	return s
}
