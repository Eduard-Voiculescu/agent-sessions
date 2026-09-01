package ui

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/eduardvoiculescu/agent-sessions/internal/action"
	"github.com/eduardvoiculescu/agent-sessions/internal/session"
	"github.com/eduardvoiculescu/agent-sessions/internal/untrusted"
)

const (
	// defaultHeight sizes the frames rendered before the first tea.WindowSizeMsg
	// arrives. The standard renderer drops lines off the top of a frame taller
	// than the terminal, so guessing too tall hides the header and the cursor.
	defaultHeight = 24

	// headerBlockLines is how many lines the bordered header box draws: the top
	// border, the counts line, the filters line, and the bottom border. There is
	// no directory line: the picker is gaining providers beyond Claude Code, so
	// one agent's state directory is not a property of the whole tool.
	headerBlockLines = 4

	// wordmarkBlockLines is how many lines the sticky wordmark draws: a blank
	// line above it, the two glyph rows, and the blank line separating them
	// from the header block.
	wordmarkBlockLines = 4

	// wordmarkGlyphTop and wordmarkGlyphBottom are the "AS" block-letter rows.
	// Both are exactly wordmarkGlyphWidth cells (lipgloss.Width, not
	// len([]rune)) wide, which wordmarkLines relies on to guarantee a cut
	// never lands inside the glyph.
	wordmarkGlyphTop    = "█▀█ █▀▀"
	wordmarkGlyphBottom = "█▀█ ▀▀█"
	wordmarkGap         = "   "

	// keyHints is the footer's full list; keyHintsCore is what survives a terminal
	// too narrow for it. Two strings rather than one truncated: cutting the full
	// list drops whatever is last, and "q quit" is the hint somebody stuck in a
	// full-screen program needs most.
	keyHints     = "⏎ preview  ^n new  r rename  ^p actions  ^j jump  ^h live  / filter  ? help  q quit"
	keyHintsCore = "⏎ preview  ^p actions  ^j jump  ^h live  / filter  ? help  q quit"
)

// wordmarkGlyphWidth is the "AS" glyph's own width in cells, both rows being
// the same width by construction. Computed rather than a literal so it never
// drifts from the constants above. wordmarkMinWidth adds the gutter the glyph
// is indented by, which is the width the glyph actually needs on screen.
var (
	wordmarkGlyphWidth = lipgloss.Width(wordmarkGlyphTop)
	wordmarkMinWidth   = gutterWidth + wordmarkGlyphWidth
)

type Choice struct {
	Session session.Session
	Fork    bool
	// Message is the opening prompt to resume with, set when a message was
	// composed for a session that has no live process to type into. Sending to
	// one is resuming it, so the picker exits the way any other resume does.
	Message string
}

// Config is everything the picker takes from the command layer.
type Config struct {
	Sessions []session.Session
	// Refresh re-reads the live registry, once per tick.
	Refresh func() []session.Session
	// Filter re-applies the command-line filters after each tick. Merge appends
	// live keys the baseline never had and demotes exited ones in place, so
	// without this a tick would widen --live or --cwd back to the whole machine.
	Filter  func([]session.Session) []session.Session
	LoadErr error
	// Actions returns the actions available for one session, already filtered by
	// availability.
	Actions func(s session.Session) []action.Action
	// Reload re-reads the full session list after an action that changed it.
	Reload func() ([]session.Session, error)
	// Commands is re-invoked on every palette open, not stored, because a
	// command can change the state that decides which commands apply — clearing
	// the cwd filter must remove its own entry.
	Commands func() []Command
	// ToggleLive flips the --live filter. The palette offers the same operation
	// as a command; this is ctrl+h's own handle on it, so the key does not have
	// to find a command by matching its label.
	ToggleLive func() (action.Result, error)
	// Jump switches focus to the terminal pane running pid, or reports why it
	// could not. It is a closure rather than a *termjump.Jumper so this package
	// never depends on iTerm2 or the darwin-only mechanism behind it, and it takes
	// a context for the same reason action.Action.Run does: it shells out, and the
	// event loop cannot wait on a subprocess that may be blocked on a human.
	Jump func(ctx context.Context, pid int) error
	// Send types a message into the terminal pane running pid, without taking
	// focus. A closure for the same reasons Jump is: this package stays free of
	// iTerm2, and the event loop cannot wait on a subprocess that may be blocked
	// on a human.
	Send func(ctx context.Context, pid int, text string) error
	// StartAgents is the agents that can be started fresh, in the order the
	// provider registry produced them. It is empty when no registered agent can
	// start one, which is what ctrl+n reports rather than opening on nothing.
	StartAgents []string
	// Start launches agent as a new terminal session in dir. A closure for the
	// same reasons Jump and Send are: this package names neither iTerm2 nor any
	// provider, and the event loop cannot wait on a subprocess that may be
	// blocked on a human.
	Start func(ctx context.Context, agent, dir string) error
	// Rename records the name an owner gave a session, or clears it when the name
	// is empty. A closure because the store is the command layer's to own, and
	// this package draws rows rather than deciding where a name is kept.
	Rename func(s session.Session, name string) error
	// Preview reads the last messages of a session's transcript, for Enter to
	// show before resuming. It runs as a tea.Cmd, not inline: like Jump, it does
	// I/O that must not block rendering, the tick or ctrl+c.
	Preview func(s session.Session) ([]session.Message, error)

	// LiveOnly and Cwd report the command-line filters back for the header
	// block's filters line, which Filter, an opaque closure, cannot be asked
	// about. They are accessors for the same reason Commands is: a palette
	// command mutates the settings the Filter closure reads live, so a value
	// snapshotted here would leave the header naming a filter the rows no
	// longer obey.
	LiveOnly func() bool
	Cwd      func() string
	// TicketPrefixes and ClaudeDir cannot change while the picker runs, so they
	// stay values. ClaudeDir is the resolved Claude Code directory, shown on the
	// header block's directory line.
	TicketPrefixes []string
	ClaudeDir      string
}

type tickMsg struct{}

// jumpResultMsg carries a finished jump back into the loop, since the jump runs
// as a tea.Cmd off the event loop.
type jumpResultMsg struct{ err error }

type model struct {
	all           []session.Session
	history       []session.Session
	refresh       func() []session.Session
	filter        func([]session.Session) []session.Session
	loadErr       error
	query         string
	filtering     bool
	cursor        int
	offset        int
	width         int
	height        int
	confirming    bool
	confirmPrompt string
	pending       session.Session
	done          bool
	chosen        bool
	choice        Choice
	actions       func(session.Session) []action.Action
	reload        func() ([]session.Session, error)
	commands      func() []Command
	toggleLive    func() (action.Result, error)
	jump          func(context.Context, int) error
	send          func(context.Context, int, string) error
	preview       func(session.Session) ([]session.Message, error)
	previewing    bool
	previewLoad   bool
	previewTarget session.Session
	previewMsgs   []session.Message
	previewOffset int
	// previewFull drops the per-message line cap, so a long message can be read
	// in full rather than only through its first screenful.
	previewFull bool
	// previewGen is bumped on every openPreview so a previewResultMsg from a
	// preview the owner has since left (esc, or a second Enter on another row)
	// cannot land on state it no longer describes.
	previewGen    int
	composing     bool
	composeText   string
	renaming      bool
	renameText    string
	renameTarget  session.Session
	rename        func(session.Session, string) error
	starting      bool
	startStep     startStep
	startAgents   []string
	startAgent    string
	startQuery    string
	startCursor   int
	startOffset   int
	startDirs     []startDir
	start         func(context.Context, string, string) error
	help          bool
	palette       bool
	paletteQuery  string
	paletteCursor int
	paletteOffset int
	paletteItems  []entry
	paletteTarget session.Session
	pendingAction action.Action
	status        string

	liveOnly       func() bool
	cwd            func() string
	ticketPrefixes []string
	claudeDir      string
}

// guarded wraps every source of session and message data the picker draws from.
// The values arrive out of an agent's own files, so they are neutralised once on
// the way in rather than at each of the two dozen places one is drawn — a render
// site added later is then safe without having to remember this.
func guarded(cfg Config) Config {
	cfg.Sessions = untrusted.Sessions(cfg.Sessions)

	if refresh := cfg.Refresh; refresh != nil {
		cfg.Refresh = func() []session.Session { return untrusted.Sessions(refresh()) }
	}
	if reload := cfg.Reload; reload != nil {
		cfg.Reload = func() ([]session.Session, error) {
			found, err := reload()
			return untrusted.Sessions(found), err
		}
	}
	if preview := cfg.Preview; preview != nil {
		cfg.Preview = func(s session.Session) ([]session.Message, error) {
			messages, err := preview(s)
			return untrusted.Messages(messages), err
		}
	}
	return cfg
}

func newModel(cfg Config) model {
	cfg = guarded(cfg)

	// Ticks merge against this liveness-stripped baseline, never against the
	// previous tick's output: Merge only writes keys present in the live set,
	// so folding its own result forward would leave an exited session marked
	// live for the rest of the run.
	return model{
		all:            cfg.Sessions,
		history:        strippedBaseline(cfg.Sessions),
		refresh:        cfg.Refresh,
		filter:         cfg.Filter,
		loadErr:        cfg.LoadErr,
		actions:        cfg.Actions,
		reload:         cfg.Reload,
		commands:       cfg.Commands,
		toggleLive:     cfg.ToggleLive,
		jump:           cfg.Jump,
		send:           cfg.Send,
		preview:        cfg.Preview,
		rename:         cfg.Rename,
		startAgents:    cfg.StartAgents,
		start:          cfg.Start,
		liveOnly:       cfg.LiveOnly,
		cwd:            cfg.Cwd,
		ticketPrefixes: cfg.TicketPrefixes,
		claudeDir:      cfg.ClaudeDir,
	}
}

func strippedBaseline(sessions []session.Session) []session.Session {
	out := make([]session.Session, len(sessions))
	for i, s := range sessions {
		s.Live, s.PID, s.Status, s.StartedAt = false, 0, "", time.Time{}
		out[i] = s
	}
	return out
}

func (m model) Init() tea.Cmd {
	return tick()
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) visible() []session.Session {
	return Filter(m.all, m.query)
}

func (m model) applyFilter(sessions []session.Session) []session.Session {
	if m.filter == nil {
		return sessions
	}
	return m.filter(sessions)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.offset = m.windowStart(m.visible())
		return m, nil

	case tickMsg:
		if m.refresh != nil {
			merged := session.Merge(m.history, m.refresh())
			session.Sort(merged)
			m.all = m.applyFilter(merged)
		}
		return m, tea.Batch(tick(), m.refreshPreviewCmd())

	case jumpResultMsg:
		// A successful jump leaves nothing to say, and clearing is the point: the
		// footer would otherwise still be explaining a failure the owner has
		// already moved on from.
		m.status = ""
		if msg.err != nil {
			m.status = msg.err.Error()
		}
		return m, nil

	case previewResultMsg:
		return m.handlePreviewResult(msg), nil

	case sendResultMsg:
		return m.handleSendResult(msg)

	case actionResultMsg:
		return m.handleActionResult(msg)

	case startResultMsg:
		return m.handleStartResult(msg)

	case tea.KeyMsg:
		// ctrl+c quits from every mode, before the mode dispatch gets a chance to
		// swallow it: a TUI that keeps running on ctrl+c reads as hung.
		if msg.Type == tea.KeyCtrlC {
			m.done = true
			return m, tea.Quit
		}
		// ctrl+p is intercepted before the mode dispatch like ctrl+c, so the row
		// filter cannot swallow it.
		if msg.Type == tea.KeyCtrlP && !m.palette && !m.confirming && !m.previewing && !m.composing && !m.renaming {
			return m.openPalette(), nil
		}
		// ctrl+n is intercepted here for the same reason ctrl+p is — the row
		// filter would otherwise swallow it — but never over a mode that owns the
		// keyboard, where an n is text somebody is typing.
		if msg.Type == tea.KeyCtrlN && !m.palette && !m.confirming && !m.previewing && !m.composing && !m.starting && !m.renaming {
			return m.openStart(), nil
		}
		if m.palette {
			return m.updatePalette(msg)
		}
		if m.confirming {
			return m.updateConfirming(msg)
		}
		if m.composing {
			return m.updateComposing(msg)
		}
		if m.renaming {
			return m.updateRenaming(msg)
		}
		if m.starting {
			return m.updateStarting(msg)
		}
		if m.previewing {
			return m.updatePreviewing(msg)
		}
		if m.filtering {
			return m.updateFiltering(msg)
		}
		return m.updateBrowsing(msg)
	}

	return m, nil
}

// updateConfirming lets only an explicit yes through: the prompt reads [y/N], and
// enter is the likeliest next keystroke after the enter that opened the prompt.
// Confirming is only ever entered from a palette action's Confirm prompt now
// that the preview has taken over Enter's own "resume anyway?" question, so
// pendingAction is always set here.
func (m model) updateConfirming(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.confirming = false
		if m.pendingAction == nil {
			return m, nil
		}
		return m.runAction(m.pendingAction, m.pending)

	case "q", "ctrl+c":
		m.confirming = false
		m.done = true
		return m, tea.Quit

	default:
		m.confirming = false
		m.pendingAction = nil
		m.pending = session.Session{}
		m.paletteTarget = session.Session{}
		m.confirmPrompt = ""
		return m, nil
	}
}

func (m model) updateFiltering(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter, tea.KeyEsc:
		m.filtering = false
	case tea.KeyBackspace:
		if m.query != "" {
			_, width := utf8.DecodeLastRuneInString(m.query)
			m.query = m.query[:len(m.query)-width]
			m.cursor, m.offset = 0, 0
		}
	// KeySpace carries the space in Runes, and Filter splits its query on
	// whitespace, so a filter that dropped spaces could never express the
	// multi-term queries it is built to match.
	case tea.KeyRunes, tea.KeySpace:
		// printable, as the compose box does: a control rune matches nothing in a
		// session's fields, and the keystrokes that produce one are bindings rather
		// than text anyone meant to type.
		m.query += printable(msg.Runes)
		m.cursor, m.offset = 0, 0
	}
	return m, nil
}

func (m model) updateBrowsing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.visible()

	switch msg.String() {
	case "q", "esc":
		m.done = true
		return m, tea.Quit

	case "j", "down":
		return m.moveBy(rows, 1), nil

	case "k", "up":
		return m.moveBy(rows, -1), nil

	case "ctrl+d":
		return m.moveBy(rows, m.halfPage(rows)), nil

	case "ctrl+u":
		return m.moveBy(rows, -m.halfPage(rows)), nil

	case "pgdown":
		return m.moveBy(rows, m.rowCapacity(rows)), nil

	case "pgup":
		return m.moveBy(rows, -m.rowCapacity(rows)), nil

	case "g":
		return m.moveTo(rows, 0), nil

	case "G":
		return m.moveTo(rows, len(rows)-1), nil

	case "/":
		m.filtering = true
		m.query = ""
		m.cursor, m.offset = 0, 0

	case "f":
		return m.choose(true)

	case "ctrl+j":
		return m.jumpToPane(rows)

	case "ctrl+h":
		return m.toggleLiveFilter()

	case "r":
		if m.cursor < len(rows) {
			return m.startRename(rows[m.cursor]), nil
		}
		return m, nil

	case "?":
		m.help = !m.help
		return m, nil

	case "enter":
		return m.openPreview(rows)
	}

	return m, nil
}

// toggleLiveFilter runs the same operation the palette offers as a command, and
// through the same path: the filter closure the rows are drawn from reads the
// flag live, so the list has to be reloaded for the change to be visible.
func (m model) toggleLiveFilter() (tea.Model, tea.Cmd) {
	if m.toggleLive == nil {
		m.status = "the --live filter is unavailable"
		return m, nil
	}
	return m.runCommand(Command{Label: "toggle --live filter", Run: m.toggleLive})
}

func (m model) moveBy(rows []session.Session, delta int) model {
	return m.moveTo(rows, m.cursor+delta)
}

// moveTo clamps the target into the row range, then scrolls the window only as
// far as keeping the cursor inside it requires.
func (m model) moveTo(rows []session.Session, index int) model {
	if len(rows) == 0 {
		m.cursor, m.offset = 0, 0
		return m
	}
	m.cursor = min(max(index, 0), len(rows)-1)
	m.offset = m.windowStart(rows)
	return m
}

func (m model) halfPage(rows []session.Session) int {
	return max(m.rowCapacity(rows)/2, 1)
}

// jumpToPane switches focus to the terminal pane running the selected row's
// process. A row with no process and an unwired jump are answered in the
// footer without shelling out at all.
func (m model) jumpToPane(rows []session.Session) (tea.Model, tea.Cmd) {
	if m.cursor >= len(rows) {
		return m, nil
	}
	// The row is captured, not re-read from the cursor inside jumpToSession: a
	// tick can re-sort the rows before the command runs, and the jump would
	// then land on whichever session slid into the slot.
	return m.jumpToSession(rows[m.cursor])
}

// jumpToSession is jumpToPane's body, factored out so the preview's own
// ctrl+j — which acts on previewTarget rather than a cursor's row — shares
// the same live/PID/unwired-jump checks instead of duplicating them. The
// jump shells out to ps and osascript, either of which can block on a human
// — macOS asks for Automation consent the first time — so it runs as a
// tea.Cmd and its outcome arrives as a jumpResultMsg; running it inline would
// freeze rendering, the tick and ctrl+c for as long as it took.
func (m model) jumpToSession(target session.Session) (tea.Model, tea.Cmd) {
	if !target.Live || target.PID <= 0 {
		m.status = "no process to jump to"
		return m, nil
	}
	if m.jump == nil {
		m.status = "jump is unavailable"
		return m, nil
	}

	jump, pid := m.jump, target.PID
	return m, func() tea.Msg { return jumpResultMsg{err: jump(context.Background(), pid)} }
}

func (m model) choose(fork bool) (tea.Model, tea.Cmd) {
	rows := m.visible()
	if m.cursor >= len(rows) {
		return m, nil
	}
	m.choice = Choice{Session: rows[m.cursor], Fork: fork}
	m.chosen = true
	m.done = true
	return m, tea.Quit
}

// frame is which furniture one rendered frame draws at this terminal height,
// and how many rows the table has left over.
type frame struct {
	wordmark bool
	header   bool
	columns  bool
	spacer   bool
	loadErr  bool
	help     bool
	footer   bool
	rows     int
	// divider is whether a line was held back for the live/history rule. It is
	// reserved whether or not the rule ends up on screen, so the row area's own
	// height is rows plus this.
	divider bool
}

// contentHeight is how many lines the row area occupies, drawn or padded. The
// footer sits on the terminal's last line at every list length, so the rows
// above it are padded out rather than letting the furniture float up under a
// short list.
func (f frame) contentHeight() int {
	if f.divider {
		return f.rows + 1
	}
	return f.rows
}

func (f frame) chromeLines() int {
	lines := 0
	if f.wordmark {
		lines += wordmarkBlockLines
	}
	if f.header {
		lines += headerBlockLines
	}
	if f.help {
		lines += helpLines
	}
	for _, drawn := range []bool{f.columns, f.spacer, f.loadErr, f.footer} {
		if drawn {
			lines++
		}
	}
	return lines
}

// frameFor gives up furniture cheapest-first until one content line fits — the
// wordmark, then the header block, then the column header, then the footer's
// blank spacer, then the load error, then the footer — because a frame taller
// than the terminal loses its top lines to the renderer, which is how the
// header and the cursor row disappear. The wordmark goes first: it is pure
// decoration, while the box beneath it is the one line a short terminal still
// needs to know how many sessions there are. One line always survives for the
// cursor's row, or for the empty-state message standing in for it, so at
// height 1 that line is the whole frame.
func (m model) frameFor(rows []session.Session) frame {
	f := frame{wordmark: true, header: true, columns: true, spacer: true, loadErr: m.loadErr != nil, help: m.help, footer: true}
	height := cmp.Or(m.height, defaultHeight)

	// The help panel is asked for explicitly, so it outranks the decoration
	// above it and the blank line below, but not the rows it exists to explain
	// how to move through.
	for _, give := range []*bool{&f.wordmark, &f.header, &f.columns, &f.spacer, &f.loadErr, &f.help, &f.footer} {
		if height-f.chromeLines() >= 1 {
			break
		}
		*give = false
	}

	f.rows = max(height-f.chromeLines(), 1)
	// The divider's line is reserved whenever the data has a boundary, in view
	// or not, so that scrolling cannot change the capacity — but only where two
	// rows can share the window, since a divider with nothing above or below it
	// draws nothing.
	if boundary(rows) >= 0 && f.rows >= 2 {
		f.rows--
		f.divider = true
	}
	return f
}

func (m model) rowCapacity(rows []session.Session) int {
	return m.frameFor(rows).rows
}

// windowStart is the index of the first row to render: the scroll offset, pulled
// only as far as needed to keep the cursor inside the window at either end.
func (m model) windowStart(rows []session.Session) int {
	return scrollWindow(m.cursor, m.offset, len(rows), m.rowCapacity(rows))
}

// scrollWindow is the paging math shared by the row list and the palette: the
// window scrolls only as far as keeping the cursor inside it requires, at
// either end, rather than snapping the cursor to an edge.
func scrollWindow(cursor, offset, total, capacity int) int {
	if total <= capacity {
		return 0
	}

	start := min(max(offset, 0), total-capacity)
	if cursor < start {
		return cursor
	}
	if cursor >= start+capacity {
		return min(cursor-capacity+1, total-capacity)
	}
	return start
}

// View picks what the frame is. The two choosers float over the list rather than
// replacing it: the palette acts on a row, and blanking the screen hid the very
// row it was acting on, while starting a session is a decision made against what
// is already running.
func (m model) View() string {
	if m.palette {
		return m.paletteModalView()
	}
	if m.starting {
		return m.startModalView()
	}
	if m.previewing {
		return m.previewView()
	}
	return m.listView()
}

// listView assembles the frame line by line, so what it draws and what frameFor
// reserved cannot drift apart.
func (m model) listView() string {
	rows := m.visible()
	f := m.frameFor(rows)
	divider := boundary(rows)
	start := m.windowStart(rows)
	end := min(start+f.rows, len(rows))
	cols := layoutFor(m.width)

	var lines []string
	if f.wordmark {
		lines = append(lines, m.wordmarkLines()...)
	}
	if f.header {
		lines = append(lines, m.headerLines()...)
	}
	if f.columns {
		lines = append(lines, columnHeaderLine(cols, m.width))
	}

	var content []string
	for i := start; i < end; i++ {
		// The divider needs the rows on both sides of it on screen to mean
		// anything, so a boundary scrolled out of the window draws nothing.
		if i == divider && i > start {
			content = append(content, rule(m.width))
		}
		content = append(content, row(rows[i], i == m.cursor, cols, m.width))
	}

	if len(rows) == 0 {
		content = append(content, dimStyle.Render(gutter("no sessions matched", m.width)))
	}
	lines = append(lines, padTo(content, f.contentHeight())...)

	if f.spacer {
		lines = append(lines, "")
	}
	if f.loadErr {
		lines = append(lines, errorLine(m.loadErr, m.width))
	}
	if f.help {
		lines = append(lines, m.helpPanel()...)
	}
	if f.footer {
		lines = append(lines, m.footerLine())
	}

	return strings.Join(lines, "\n")
}

func (m model) footerLine() string {
	switch {
	// All three carry a session's own fields: the prompt names it, the status is
	// an action's report on it, and the query is matched against it. They are
	// neutralised here rather than where they are set because these are the only
	// places the strings are drawn, and gutter is handed plain text — a value
	// already carrying lipgloss's own styling must never be filtered.
	case m.confirming:
		return gutter(fmt.Sprintf("%s [y/N] ", untrusted.Text(m.confirmPrompt)), m.width)
	case m.renaming:
		return m.renameLine()
	case m.filtering:
		return gutter(fmt.Sprintf("filter: %s_", untrusted.Text(m.query)), m.width)
	case m.status != "":
		return gutter(untrusted.Text(m.status), m.width)
	default:
		// A background agent's progress line is a sentence, and the status column
		// is two dozen cells. The footer is the one full-width line available, so
		// the cursor's row lends it one — the keys are a keystroke away under ?.
		if detail := m.cursorDetail(); detail != "" {
			return dimStyle.Render(gutter("◆ "+detail, m.width))
		}
		return dimStyle.Render(gutter(hintsFor(m.width), m.width))
	}
}

// hintsFor picks the widest list that fits. It measures rather than counts
// columns, so a hint added to either string cannot silently push the last one off
// the end — which is what a test at eighty columns caught it doing.
func hintsFor(width int) string {
	if width <= 0 || lipgloss.Width(keyHints) <= span(width) {
		return keyHints
	}
	return keyHintsCore
}

// cursorDetail is the progress line of the row under the cursor, or "" for a
// row that is not a background agent — which is nearly all of them.
func (m model) cursorDetail() string {
	rows := m.visible()
	if m.cursor >= len(rows) {
		return ""
	}
	return rows[m.cursor].Detail
}

// wordmarkLines always returns wordmarkBlockLines lines, which is what
// frameFor counted against the terminal height before this was called. It
// names no directory: the picker is gaining providers beyond Claude Code, so a
// single agent's directory is not a property of the whole tool the way it is
// of the header block's own dir line.
func (m model) wordmarkLines() []string {
	top := fmt.Sprintf("agent-sessions v%s", Version)
	bottom := fmt.Sprintf("%d sessions", len(m.all))

	// A block glyph cut mid-character reads as broken art rather than a smaller
	// wordmark, so below the width the glyph itself needs, the glyph is dropped
	// and only the text survives; truncate only ever cuts whole graphemes, so
	// text alone degrades the same way every other line in this file does.
	if m.width <= 0 || m.width > wordmarkMinWidth {
		top = wordmarkGlyphTop + wordmarkGap + top
		bottom = wordmarkGlyphBottom + wordmarkGap + bottom
	}
	return []string{"", gutter(top, m.width), gutter(bottom, m.width), ""}
}

// headerLines always returns headerBlockLines lines, which is what frameFor
// counted against the terminal height before this was called.
func (m model) headerLines() []string {
	const title = "agent-sessions"
	counts := m.countsLine()
	filters := m.filtersLine()

	// The box spans exactly as far as the table's rows do, so k9s-style the
	// two frames read as one unit; content wider than that (a verbose live
	// status, say) is truncated by boxRow rather than growing the box.
	width := tableWidth(m.width)
	if m.width > 0 {
		width = min(width, m.width)
	}
	return []string{
		dimStyle.Render(boxTop(title, width)),
		boxRow(counts, width),
		boxRow(filters, width),
		dimStyle.Render(boxBottom(width)),
	}
}

// countsLine is derived from m.all rather than from the rows on screen, so it
// keeps counting every session loaded while a / query hides most of them;
// filtersLine carries that query, so the disagreement is explained rather than
// mysterious.
func (m model) countsLine() string {
	history := 0
	var order []string
	counts := map[string]int{}
	for _, s := range m.all {
		if !s.Live {
			history++
			continue
		}
		status := cmp.Or(s.Status, "active")
		if counts[status] == 0 {
			order = append(order, status)
		}
		counts[status]++
	}

	// ○ history leads because the line is truncated tail-first and it is the one
	// entry present in every frame; the live statuses vary in both number and
	// length, so they are what a narrow terminal can afford to lose.
	parts := make([]string, 0, len(order)+1)
	parts = append(parts, fmt.Sprintf("○ history %d", history))
	for _, status := range order {
		parts = append(parts, fmt.Sprintf("● %s %d", status, counts[status]))
	}

	return fmt.Sprintf("sessions %d    %s", len(m.all), strings.Join(parts, "   "))
}

// filtersLine reads the command-line filters through Config's accessors, so a
// palette command that toggles --live or clears --cwd is reflected on the next
// frame instead of the line reporting what the filters were when the picker
// started.
func (m model) filtersLine() string {
	parts := []string{
		"--live " + onOff(m.liveOnlyFilter()),
		"--cwd " + cmp.Or(m.cwdFilter(), "—"),
		"ticket " + ticketLabel(m.ticketPrefixes),
	}
	// A / query outlives filter mode, and the footer only shows it while that
	// mode is open, so this is the only place left that can account for rows
	// the counts line still counts but the table no longer shows.
	if m.query != "" {
		parts = append([]string{"query " + untrusted.Text(m.query)}, parts...)
	}
	return "filters  " + strings.Join(parts, "   ")
}

func (m model) liveOnlyFilter() bool {
	return m.liveOnly != nil && m.liveOnly()
}

func (m model) cwdFilter() string {
	if m.cwd == nil {
		return ""
	}
	return m.cwd()
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func ticketLabel(prefixes []string) string {
	if len(prefixes) == 0 {
		return "—"
	}
	upper := make([]string, len(prefixes))
	for i, p := range prefixes {
		upper[i] = strings.ToUpper(p)
	}
	return strings.Join(upper, "/")
}

// Run shows the picker and returns the chosen session. The second result is
// false when the user quit without choosing.
func Run(cfg Config) (Choice, bool, error) {
	final, err := tea.NewProgram(newModel(cfg), tea.WithAltScreen()).Run()
	if err != nil {
		return Choice{}, false, fmt.Errorf("running picker: %w", err)
	}

	m, ok := final.(model)
	if !ok || !m.chosen {
		return Choice{}, false, nil
	}
	return m.choice, true, nil
}
