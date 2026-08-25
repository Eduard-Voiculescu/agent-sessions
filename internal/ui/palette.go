package ui

import (
	"cmp"
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eduardvoiculescu/agent-sessions/internal/action"
	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

// Command is a palette entry that acts on the picker rather than on a session.
type Command struct {
	Label string
	Run   func() (action.Result, error)
}

// entry is one drawn line of the palette: a group header, the blank line
// separating two groups, or something runnable. The headers and blanks are
// entries rather than a rendering flourish so the window, the capacity
// accounting and the cursor all keep working in units of one line.
type entry struct {
	label   string
	group   action.Group
	header  bool
	blank   bool
	act     action.Action
	command *Command
}

func (e entry) runnable() bool { return e.act != nil || e.command != nil }

func (m model) openPalette() model {
	rows := m.visible()
	if m.cursor >= len(rows) {
		return m
	}

	// The target is captured by value: a tick re-sorts the rows, so anything
	// resolved from the cursor after a message boundary can act on another
	// session.
	m.paletteTarget = rows[m.cursor]
	m.palette = true
	m.paletteQuery = ""
	m.status = ""
	return m.snapshotEntries()
}

// snapshotEntries freezes the entry list alongside the target. Availability is
// live — killAction asks the OS whether the pid still exists — so recomputing
// the list per message would let an entry vanish between the render and the
// keypress, sliding the next one, delete session, into the slot the user aimed
// at.
func (m model) snapshotEntries() model {
	m.paletteItems = m.buildPaletteEntries()
	m.paletteOffset = 0
	// The first entry is a group header, never something to run.
	m.paletteCursor = max(m.nearestRunnable(0, 1), 0)
	return m
}

func (m model) buildPaletteEntries() []entry {
	var runnable []entry
	if m.actions != nil {
		for _, a := range m.actions(m.paletteTarget) {
			runnable = append(runnable, entry{label: a.Label(m.paletteTarget), group: a.Group(), act: a})
		}
	}
	if m.commands != nil {
		cmds := m.commands()
		for i := range cmds {
			// A command acts on the picker, not on the session the palette was
			// opened for, which is the whole reason it sits in its own group.
			runnable = append(runnable, entry{label: cmds[i].Label, group: action.GroupPicker, command: &cmds[i]})
		}
	}

	if terms := strings.Fields(strings.ToLower(m.paletteQuery)); len(terms) > 0 {
		kept := make([]entry, 0, len(runnable))
		for _, e := range runnable {
			if matchesAll(strings.ToLower(e.label), terms) {
				kept = append(kept, e)
			}
		}
		runnable = kept
	}
	return withGroupHeaders(runnable)
}

// withGroupHeaders inserts one header per group that has members, in group
// order, separated by a blank line. A group the filter emptied loses its header
// too: a heading over nothing reads as an entry that failed to draw. Order
// within a group is the order the registry produced, so an action's position
// never depends on its label.
func withGroupHeaders(runnable []entry) []entry {
	if len(runnable) == 0 {
		return nil
	}

	grouped := make([]entry, 0, len(runnable)+2*len(action.Groups()))
	for _, g := range action.Groups() {
		var members []entry
		for _, e := range runnable {
			if e.group == g {
				members = append(members, e)
			}
		}
		if len(members) == 0 {
			continue
		}
		if len(grouped) > 0 {
			grouped = append(grouped, entry{blank: true})
		}
		grouped = append(grouped, entry{header: true, label: g.String(), group: g})
		grouped = append(grouped, members...)
	}
	return grouped
}

func (m model) updatePalette(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.palette = false
		m.paletteTarget = session.Session{}
		m.paletteItems = nil
		return m, nil

	case tea.KeyUp:
		return m.movePaletteBy(-1), nil

	case tea.KeyDown:
		return m.movePaletteBy(1), nil

	case tea.KeyCtrlD:
		return m.movePaletteBy(m.paletteHalfPage()), nil

	case tea.KeyCtrlU:
		return m.movePaletteBy(-m.paletteHalfPage()), nil

	case tea.KeyBackspace:
		if m.paletteQuery != "" {
			m.paletteQuery = trimLastRune(m.paletteQuery)
			m = m.snapshotEntries()
		}
		return m, nil

	// KeySpace already carries the space in Runes, so appending one again would
	// type two spaces per keypress and take two backspaces to undo.
	case tea.KeyRunes, tea.KeySpace:
		m.paletteQuery += string(msg.Runes)
		return m.snapshotEntries(), nil

	case tea.KeyEnter:
		if m.paletteCursor >= len(m.paletteItems) || !m.paletteItems[m.paletteCursor].runnable() {
			return m, nil
		}
		return m.launch(m.paletteItems[m.paletteCursor])
	}

	return m, nil
}

func (m model) movePaletteBy(delta int) model {
	return m.movePaletteTo(m.paletteCursor + delta)
}

// movePaletteTo mirrors moveTo for the row list: clamp the target into the
// entry range, then scroll only as far as keeping the cursor inside the
// window requires.
func (m model) movePaletteTo(index int) model {
	if len(m.paletteItems) == 0 {
		m.paletteCursor, m.paletteOffset = 0, 0
		return m
	}

	step := 1
	if index < m.paletteCursor {
		step = -1
	}
	target := m.nearestRunnable(min(max(index, 0), len(m.paletteItems)-1), step)
	if target < 0 {
		return m
	}

	m.paletteCursor = target
	m.paletteOffset = m.paletteWindowStart(len(m.paletteItems))
	return m
}

// nearestRunnable walks from index in the direction of travel, then back the
// other way. Both passes are needed: a half-page jump lands wherever the
// arithmetic puts it, which at the end of the list is a header, and stalling
// the cursor on one would make the key look broken.
func (m model) nearestRunnable(index, step int) int {
	for i := index; i >= 0 && i < len(m.paletteItems); i += step {
		if m.paletteItems[i].runnable() {
			return i
		}
	}
	for i := index; i >= 0 && i < len(m.paletteItems); i -= step {
		if m.paletteItems[i].runnable() {
			return i
		}
	}
	return -1
}

func (m model) paletteHalfPage() int {
	return max(m.paletteCapacity()/2, 1)
}

// paletteTitleLines is the palette's own title plus the blank line under it.
// The palette draws neither the row list's bordered header block nor its column
// header, so its furniture is counted independently of frame.
const paletteTitleLines = 2

// paletteFrame is frame's counterpart for the palette: which of its furniture
// one rendered frame draws, and how many entry lines are left over.
type paletteFrame struct {
	title   bool
	spacer  bool
	footer  bool
	filter  bool
	entries int
}

func (f paletteFrame) chromeLines() int {
	lines := 0
	if f.title {
		lines += paletteTitleLines
	}
	for _, drawn := range []bool{f.spacer, f.footer, f.filter} {
		if drawn {
			lines++
		}
	}
	return lines
}

// paletteFrameFor surrenders furniture cheapest-first like frameFor does, and
// for the same reason: a frame taller than the terminal loses its top lines to
// the renderer. The filter line goes last of the four because it is the only one
// showing something the user typed. One entry line always survives.
func (m model) paletteFrameFor() paletteFrame {
	f := paletteFrame{title: true, spacer: true, footer: true, filter: m.paletteQuery != ""}
	height := cmp.Or(m.height, defaultHeight)

	for _, give := range []*bool{&f.title, &f.spacer, &f.footer, &f.filter} {
		if height-f.chromeLines() >= 1 {
			break
		}
		*give = false
	}

	f.entries = max(height-f.chromeLines(), 1)
	return f
}

func (m model) paletteCapacity() int {
	return m.paletteFrameFor().entries
}

// paletteWindowStart is windowStart for the palette: the same scrollWindow
// math, over the entry list instead of the row list.
func (m model) paletteWindowStart(total int) int {
	return scrollWindow(m.paletteCursor, m.paletteOffset, total, m.paletteCapacity())
}

func (m model) launch(e entry) (tea.Model, tea.Cmd) {
	if e.act != nil {
		if prompt := e.act.Confirm(m.paletteTarget); prompt != "" {
			m.palette = false
			m.paletteItems = nil
			m.confirming = true
			m.pendingAction = e.act
			m.pending = m.paletteTarget
			m.confirmPrompt = prompt
			return m, nil
		}
		return m.runAction(e.act, m.paletteTarget)
	}
	return m.runCommand(*e.command)
}

// actionTimeout bounds one action end to end. Most act on the local filesystem
// and finish at once, but "open PR on GitHub" is a network round trip, and a
// hung one must not leave the footer saying "working…" forever.
const actionTimeout = 15 * time.Second

// runAction hands the action to a tea.Cmd rather than running it here. An
// action can reach the network — asking GitHub which pull request a branch
// belongs to — and anything run inside Update freezes rendering, the tick and
// ctrl+c until it returns.
func (m model) runAction(a action.Action, target session.Session) (tea.Model, tea.Cmd) {
	m = m.closePalette()
	m.pendingAction = nil
	m.status = cmp.Or(a.Label(target), "working") + "…"

	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()

		res, err := a.Run(ctx, target)
		return actionResultMsg{result: res, err: err}
	}
}

// actionResultMsg carries a finished action back into the loop.
type actionResultMsg struct {
	result action.Result
	err    error
}

func (m model) handleActionResult(msg actionResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = msg.err.Error()
		return m, nil
	}
	// A blank Result.Status still needs a visible footer: silence there reads as
	// the keypress having been dropped.
	m.status = cmp.Or(msg.result.Status, "done")
	if msg.result.Refresh {
		m = m.reloadSessions()
	}
	return m, nil
}

// closePalette leaves the row filter's editing mode too: the footer draws the
// filter line ahead of the status, so an action's result would be invisible if
// the palette had been opened from there.
func (m model) closePalette() model {
	m.palette = false
	m.paletteItems = nil
	m.filtering = false
	return m
}

func (m model) runCommand(c Command) (tea.Model, tea.Cmd) {
	m = m.closePalette()

	res, err := c.Run()
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.status = res.Status
	m = m.reloadSessions()
	return m, nil
}

func (m model) reloadSessions() model {
	if m.reload == nil {
		return m
	}

	sessions, err := m.reload()
	// A non-empty slice may arrive with an error — one unreadable transcript
	// among hundreds — and the error belongs on the load-error line, never in
	// the status, which is carrying what the destructive action just did. Only a
	// reload that yielded nothing at all leaves the previous list standing.
	m.loadErr = err
	if err != nil && len(sessions) == 0 {
		return m
	}

	m.all = sessions
	m.history = strippedBaseline(sessions)
	rows := m.visible()
	if m.cursor >= len(rows) {
		m.cursor = max(len(rows)-1, 0)
	}
	m.offset = 0
	return m
}

func (m model) paletteView() string {
	entries := m.paletteItems
	f := m.paletteFrameFor()
	start := m.paletteWindowStart(len(entries))
	end := min(start+f.entries, len(entries))

	var lines []string
	if f.title {
		lines = append(lines, headerStyle.Render(m.paletteLine("  actions — "+m.paletteTarget.Name)), "")
	}
	var content []string
	for i := start; i < end; i++ {
		e := entries[i]
		switch {
		case e.blank:
			content = append(content, "")
		case e.header:
			content = append(content, dimStyle.Render(m.paletteLine("  "+e.label)))
		default:
			marker := "    "
			label := e.label
			if m.width > 0 {
				label = truncate(label, max(m.width-4, 1))
			}
			if i == m.paletteCursor {
				marker = "  ▸ "
				label = selectedStyle.Render(label)
			}
			content = append(content, m.paletteLine(marker+label))
		}
	}
	if len(entries) == 0 {
		content = append(content, dimStyle.Render(m.paletteLine("    no matching action")))
	}
	lines = append(lines, padTo(content, f.entries)...)

	if f.spacer {
		lines = append(lines, "")
	}
	if f.footer {
		lines = append(lines, dimStyle.Render(m.paletteLine("  ↑/↓ ^u/^d move · type to filter · enter run · esc back")))
	}
	if f.filter {
		lines = append(lines, dimStyle.Render(m.paletteLine("  filter: "+m.paletteQuery)))
	}
	return strings.Join(lines, "\n")
}

// paletteLine stops a line at the window edge, so a session name — which is the
// first user prompt, and bounded by no column here — cannot wrap and cost a
// second line. width is 0 until the first tea.WindowSizeMsg arrives, and
// truncating against that would blank the line.
func (m model) paletteLine(text string) string {
	if m.width <= 0 {
		return text
	}
	return truncate(text, m.width)
}

func trimLastRune(s string) string {
	runes := []rune(s)
	return string(runes[:len(runes)-1])
}
