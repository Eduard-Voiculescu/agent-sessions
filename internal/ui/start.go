package ui

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

// startStep is which of the mode's two questions is on screen: which agent,
// then which directory.
type startStep int

const (
	startStepAgent startStep = iota
	startStepDir
)

// startDir is one directory the mode offers. sessions is how many loaded
// sessions sit in it, which is what ranks the list; typed marks a path written
// by hand, which is offered without being on the list at all.
type startDir struct {
	path     string
	sessions int
	typed    bool
}

// startResultMsg carries a finished spawn back into the loop, since it shells
// out to osascript and blocks on the Automation consent dialog the first time.
// agent and dir travel with it so a failure can name what it could not start
// and hand the path back rather than have it found again.
type startResultMsg struct {
	agent string
	dir   string
	err   error
}

// openStart asks which agent first, because the answer is one of two or three
// names while the directory is a list long enough to need filtering — and
// nothing about the directory step depends on knowing the agent, so the cheap
// question goes first.
func (m model) openStart() model {
	if len(m.startAgents) == 0 {
		m.status = "no agent can start a session"
		return m
	}

	m.starting = true
	m.startStep = startStepAgent
	m.startAgent = ""
	m.startQuery = ""
	m.startCursor, m.startOffset = 0, 0
	m.status = ""
	return m
}

func (m model) closeStart() model {
	m.starting = false
	m.startDirs = nil
	m.startQuery = ""
	m.startCursor, m.startOffset = 0, 0
	return m
}

// openStartDirs snapshots the directories the way the palette snapshots its
// entries, and for the same reason: a tick re-sorts the sessions, so a list
// recomputed per message would let a directory slide under the cursor between
// the render and the keypress.
func (m model) openStartDirs() model {
	m.startStep = startStepDir
	m.startQuery = ""
	m.startCursor, m.startOffset = 0, 0
	m.startDirs = startDirsOf(m.all)
	return m
}

// startDirsOf ranks the distinct directories of the loaded sessions by how many
// sessions each holds, then by path so that two with the same count hold a
// stable order instead of map order.
func startDirsOf(sessions []session.Session) []startDir {
	counts := map[string]int{}
	for _, s := range sessions {
		if s.Cwd != "" {
			counts[s.Cwd]++
		}
	}

	dirs := make([]startDir, 0, len(counts))
	for path, held := range counts {
		dirs = append(dirs, startDir{path: path, sessions: held})
	}
	slices.SortFunc(dirs, func(a, b startDir) int {
		return cmp.Or(cmp.Compare(b.sessions, a.sessions), cmp.Compare(a.path, b.path))
	})
	return dirs
}

// startCandidates is what the directory step offers right now: the snapshot
// filtered by the query, with a typed path first. Only how a query begins can
// tell a path from a filter term, so "api" narrows the list and "~/git/api"
// offers itself.
func (m model) startCandidates() []startDir {
	var candidates []startDir
	if looksLikePath(m.startQuery) {
		candidates = append(candidates, startDir{path: expandHome(m.startQuery), typed: true})
	}

	terms := strings.Fields(strings.ToLower(m.startQuery))
	for _, dir := range m.startDirs {
		if matchesAll(strings.ToLower(dir.path), terms) {
			candidates = append(candidates, dir)
		}
	}
	return candidates
}

func looksLikePath(query string) bool {
	return strings.HasPrefix(query, "/") || strings.HasPrefix(query, "~") || strings.HasPrefix(query, ".")
}

// expandHome resolves a leading ~ the way the config parser does: no shell has
// read what was typed here, so a ~ would otherwise reach os.Stat as a literal
// character and the start would fail on a path the owner typed correctly.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

func (m model) updateStarting(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		// esc walks the mode back a step before leaving it: the agent is the
		// cheaper answer to change, and closing outright would make choosing it
		// again the price of a mistyped directory.
		if m.startStep == startStepDir {
			m.startStep = startStepAgent
			m.startCursor = max(slices.Index(m.startAgents, m.startAgent), 0)
			m.startOffset = 0
			return m, nil
		}
		return m.closeStart(), nil

	case tea.KeyUp:
		return m.moveStartBy(-1), nil

	case tea.KeyDown:
		return m.moveStartBy(1), nil

	case tea.KeyCtrlD:
		return m.moveStartBy(max(m.startCapacity()/2, 1)), nil

	case tea.KeyCtrlU:
		return m.moveStartBy(-max(m.startCapacity()/2, 1)), nil

	case tea.KeyBackspace:
		if m.startStep == startStepDir && m.startQuery != "" {
			m.startQuery = trimLastRune(m.startQuery)
			m.startCursor, m.startOffset = 0, 0
		}
		return m, nil

	// KeySpace carries the space in Runes, and a directory name may contain one.
	case tea.KeyRunes, tea.KeySpace:
		if m.startStep == startStepDir {
			m.startQuery += printable(msg.Runes)
			m.startCursor, m.startOffset = 0, 0
		}
		return m, nil

	case tea.KeyEnter:
		if m.startStep == startStepAgent {
			return m.chooseStartAgent(), nil
		}
		return m.startChosen()
	}

	return m, nil
}

func (m model) chooseStartAgent() model {
	if m.startCursor >= len(m.startAgents) {
		return m
	}
	m.startAgent = m.startAgents[m.startCursor]
	return m.openStartDirs()
}

// startChosen hands the spawn to a tea.Cmd rather than running it here: it
// shells out to osascript, and anything run inside Update freezes rendering,
// the tick and ctrl+c until it returns. The mode closes first, so three
// sessions can be started in a row without waiting out each consent prompt.
func (m model) startChosen() (tea.Model, tea.Cmd) {
	candidates := m.startCandidates()
	if m.startCursor >= len(candidates) {
		return m, nil
	}
	if m.start == nil {
		m.status = "start is unavailable"
		return m, nil
	}

	agent, dir := m.startAgent, candidates[m.startCursor].path
	m = m.closeStart()
	m.status = fmt.Sprintf("starting %s in %s…", agent, shortenHome(dir))

	start := m.start
	return m, func() tea.Msg {
		return startResultMsg{agent: agent, dir: dir, err: start(context.Background(), agent, dir)}
	}
}

// handleStartResult reports the outcome. Nothing is added to the row list on
// success: the agent writes its own live-registry entry, so the session arrives
// on the next tick with a pid this program never had to guess at.
func (m model) handleStartResult(msg startResultMsg) (tea.Model, tea.Cmd) {
	if msg.err == nil {
		m.status = fmt.Sprintf("started %s in %s", msg.agent, shortenHome(msg.dir))
		return m, nil
	}

	m.status = msg.err.Error()
	// Every failure this can report is one the owner may be able to correct — a
	// directory since removed, consent not yet granted, iTerm2 not running — so
	// the step reopens holding the path instead of discarding it.
	m.starting = true
	m.startStep = startStepDir
	m.startAgent = msg.agent
	m.startDirs = startDirsOf(m.all)
	m.startQuery = msg.dir
	m.startCursor, m.startOffset = 0, 0
	return m, nil
}

func (m model) moveStartBy(delta int) model {
	total := len(m.startAgents)
	if m.startStep == startStepDir {
		total = len(m.startCandidates())
	}
	if total == 0 {
		m.startCursor, m.startOffset = 0, 0
		return m
	}

	m.startCursor = min(max(m.startCursor+delta, 0), total-1)
	m.startOffset = scrollWindow(m.startCursor, m.startOffset, total, m.startCapacity())
	return m
}

// startFrame is which of the mode's furniture one rendered frame draws, and how
// many entry lines are left over. It mirrors paletteFrame because a frame taller
// than the terminal loses its top lines to the renderer.
type startFrame struct {
	title   bool
	spacer  bool
	footer  bool
	query   bool
	entries int
}

func (f startFrame) chromeLines() int {
	lines := 0
	if f.title {
		lines += paletteTitleLines
	}
	for _, drawn := range []bool{f.spacer, f.footer, f.query} {
		if drawn {
			lines++
		}
	}
	return lines
}

// startFrameFor surrenders furniture cheapest-first, the query line last of the
// four: it is the only one showing something the owner typed.
func (m model) startFrameFor() startFrame {
	f := startFrame{title: true, spacer: true, footer: true, query: m.startStep == startStepDir}
	height := cmp.Or(m.height, defaultHeight)

	for _, give := range []*bool{&f.title, &f.spacer, &f.footer, &f.query} {
		if height-f.chromeLines() >= 1 {
			break
		}
		*give = false
	}

	f.entries = max(height-f.chromeLines(), 1)
	return f
}

// startCapacity is how many entries fit wherever the chooser is actually drawn,
// so the cursor's paging math and the render cannot disagree about the window.
func (m model) startCapacity() int {
	if f := m.startModalFrame(); f.width != 0 {
		return f.items
	}
	return m.startFrameFor().entries
}

// startChrome is how many of the box's inner lines are not entries: the hint and
// the blank above it, plus the search line and its own blank on the step that
// has one. The agent step has no search line — the answer is one of two or three
// names, and a filter over that is furniture rather than help.
func (m model) startChrome() int {
	if m.startStep == startStepDir {
		return 4
	}
	return 2
}

func (m model) startModalFrame() modalFrame {
	return modalFrameFor(m.width, cmp.Or(m.height, defaultHeight), m.startChrome(), len(m.startModalItems()))
}

// startModalView floats the chooser over the session list. Below the size a box
// needs, the full-screen chooser stands in — the same cheapest-first surrender
// the rest of this package makes.
func (m model) startModalView() string {
	f := m.startModalFrame()
	if f.width == 0 {
		return m.startView()
	}

	inner := modalInnerWidth(f.width)
	var head []string
	if m.startStep == startStepDir {
		head = []string{modalSearchLine(m.startQuery, inner), ""}
	}

	content := modalContent(m.startModalItems(), m.startCursor, m.startOffset, f.items, inner,
		head, "no directory matched — type a path to start somewhere new", m.startHints())
	return modalOver(m.listView(), m.startTitle(), content, f.width, m.width, cmp.Or(m.height, defaultHeight))
}

// startModalItems is the one list both views draw from, so the box and the
// full-screen fallback can never offer different things.
func (m model) startModalItems() []modalItem {
	if m.startStep == startStepAgent {
		items := make([]modalItem, 0, len(m.startAgents))
		for i, agent := range m.startAgents {
			items = append(items, modalItem{label: agent, selected: i == m.startCursor})
		}
		return items
	}

	candidates := m.startCandidates()
	items := make([]modalItem, 0, len(candidates))
	for i, dir := range candidates {
		items = append(items, modalItem{label: shortenHome(dir.path), note: startNote(dir), selected: i == m.startCursor})
	}
	return items
}

func (m model) startView() string {
	f := m.startFrameFor()
	items := m.startItems()
	start := scrollWindow(m.startCursor, m.startOffset, len(items), f.entries)
	end := min(start+f.entries, len(items))

	var lines []string
	if f.title {
		lines = append(lines, headerStyle.Render(m.paletteLine("  "+m.startTitle())), "")
	}

	var content []string
	for i := start; i < end; i++ {
		marker := "    "
		label := items[i]
		if m.width > 0 {
			label = truncate(label, max(m.width-4, 1))
		}
		if i == m.startCursor {
			marker = "  ▸ "
			label = selectedStyle.Render(label)
		}
		content = append(content, m.paletteLine(marker+label))
	}
	if len(items) == 0 {
		content = append(content, dimStyle.Render(m.paletteLine("    no directory matched — type a path to start somewhere new")))
	}
	lines = append(lines, padTo(content, f.entries)...)

	if f.spacer {
		lines = append(lines, "")
	}
	if f.footer {
		lines = append(lines, dimStyle.Render(m.paletteLine("  "+m.startHints())))
	}
	if f.query {
		lines = append(lines, dimStyle.Render(m.paletteLine("  dir: "+m.startQuery+"_")))
	}
	return strings.Join(lines, "\n")
}

func (m model) startTitle() string {
	if m.startStep == startStepAgent {
		return "start a session — which agent"
	}
	return "start " + m.startAgent + " — where"
}

func (m model) startHints() string {
	if m.startStep == startStepAgent {
		return "↑/↓ move · enter choose · esc cancel"
	}
	return "↑/↓ move · type a filter or a path · enter start · esc back"
}

func (m model) startItems() []string {
	items := make([]string, 0, len(m.startModalItems()))
	for _, item := range m.startModalItems() {
		label := item.label
		if item.note != "" {
			label += "   " + item.note
		}
		items = append(items, label)
	}
	return items
}

// startNote says where an entry came from, because the list mixes two kinds: a
// directory the sessions imply, and one that exists only because it was typed —
// and only the second can turn out not to exist at all.
func startNote(dir startDir) string {
	switch {
	case dir.typed:
		return "typed"
	case dir.sessions == 1:
		return "1 session"
	default:
		return fmt.Sprintf("%d sessions", dir.sessions)
	}
}
