package ui

import (
	"cmp"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
	"github.com/eduardvoiculescu/agent-sessions/internal/untrusted"
)

// previewHeaderLines is how many lines the preview's bordered header box
// draws: the top border, one line combining directory and branch, and the
// bottom border. It is a third of the row list's headerBlockLines because the
// preview names one session, not the whole picker's filters. A background
// agent's own progress line, when there is one, adds a fourth — it is a
// sentence, so it gets its own line rather than being crushed into the status
// column's two dozen cells.
const (
	previewHeaderLines       = 3
	previewHeaderDetailLines = 4
)

// previewResultMsg carries a finished transcript read back into the loop,
// since Preview runs as a tea.Cmd off the event loop: reading a transcript
// inline would freeze rendering, the tick and ctrl+c for as long as the read
// took. gen is the previewGen the read was started under, so a result from a
// preview the owner has since left cannot land on state it no longer
// describes.
type previewResultMsg struct {
	gen      int
	messages []session.Message
	err      error
	// refresh marks a re-read of a preview already on screen rather than the
	// first read of a new one. The two land differently: a first read jumps to
	// the newest message, a refresh only follows the tail if the owner was
	// already sitting on it, and a refresh that fails leaves what is on screen
	// alone instead of closing the preview.
	refresh bool
}

// openPreview captures the row Enter was pressed on by value, the same
// discipline the confirm prompt this replaces already followed: a tick can
// re-sort the rows before the read finishes, or before the second Enter that
// resumes it, and neither may act on whatever slid into the cursor's slot
// instead.
func (m model) openPreview(rows []session.Session) (tea.Model, tea.Cmd) {
	if m.cursor >= len(rows) {
		return m, nil
	}
	target := rows[m.cursor]

	m.previewing = true
	m.previewLoad = true
	m.previewTarget = target
	m.previewMsgs = nil
	m.previewOffset = 0
	m.previewGen++
	gen := m.previewGen

	if m.preview == nil {
		m.previewLoad = false
		return m, nil
	}

	preview := m.preview
	return m, func() tea.Msg {
		messages, err := preview(target)
		return previewResultMsg{gen: gen, messages: messages, err: err}
	}
}

// handlePreviewResult applies a finished read. A first read that fails lands in
// the row list's own footer rather than inside the preview: there is nothing
// useful to scroll, so the picker returns to a screen that still works instead
// of parking the owner on a broken one. A refresh that fails says so and
// changes nothing else — the transcript is being appended to as it is read, and
// one torn read must not throw the owner out of a preview that is working.
func (m model) handlePreviewResult(msg previewResultMsg) model {
	if msg.gen != m.previewGen {
		return m
	}
	m.previewLoad = false
	if msg.err != nil {
		m.status = msg.err.Error()
		if !msg.refresh {
			m.previewing = false
		}
		return m
	}

	// Measured against the messages already on screen, before they are replaced:
	// an owner reading the tail is carried along as it grows, and one who has
	// scrolled up to read something is left where they are.
	following := !msg.refresh || m.previewOffset >= m.previewBottom()

	m.previewMsgs = msg.messages
	if following {
		m.previewOffset = m.previewBottom()
	} else {
		m.previewOffset = clampOffset(m.previewOffset, len(m.previewLines()), m.previewCapacity())
	}
	return m
}

// refreshPreviewCmd re-reads the previewed transcript in the background, so a
// reply typed into the pane shows up here as it arrives. Only a live session's
// transcript can grow, so a history preview is left alone rather than re-read
// once a second for a file that cannot change. previewLoad doubles as the
// in-flight flag: a read slower than the tick would otherwise stack up behind
// itself.
func (m model) refreshPreviewCmd() tea.Cmd {
	if !m.previewing || m.previewLoad || m.preview == nil || !m.previewTarget.Live {
		return nil
	}

	preview, target, gen := m.preview, m.previewTarget, m.previewGen
	return func() tea.Msg {
		messages, err := preview(target)
		return previewResultMsg{gen: gen, messages: messages, err: err, refresh: true}
	}
}

func (m model) updatePreviewing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A send confirmation or a jump error has been read by the time the next key
	// arrives, and leaving it up costs the footer's hint line for the rest of the
	// session.
	m.status = ""

	switch msg.String() {
	case "esc":
		m.previewing = false
		m.previewMsgs = nil
		m.previewTarget = session.Session{}
		return m, nil

	case "enter":
		m.choice = Choice{Session: m.previewTarget}
		m.chosen = true
		m.done = true
		return m, tea.Quit

	case "up":
		return m.movePreviewBy(-1), nil

	case "down":
		return m.movePreviewBy(1), nil

	case "ctrl+u":
		return m.movePreviewBy(-m.previewHalfPage()), nil

	case "ctrl+d":
		return m.movePreviewBy(m.previewHalfPage()), nil

	case "ctrl+j":
		return m.jumpToSession(m.previewTarget)

	case "i":
		return m.startComposing(), nil

	case "e":
		// The offset is left alone: expanding shifts every line below the first
		// long message, so there is no position to preserve that would mean the
		// same thing afterwards.
		m.previewFull = !m.previewFull
		m.previewOffset = clampOffset(m.previewOffset, len(m.previewLines()), m.previewCapacity())
		return m, nil
	}

	return m, nil
}

func (m model) movePreviewBy(delta int) model {
	lines := len(m.previewLines())
	m.previewOffset = clampOffset(m.previewOffset+delta, lines, m.previewCapacity())
	return m
}

func (m model) previewHalfPage() int {
	return max(m.previewCapacity()/2, 1)
}

// previewBottom is the offset that shows the newest messages, which is where
// a freshly loaded preview starts: newest at the bottom, like a terminal.
func (m model) previewBottom() int {
	lines := len(m.previewLines())
	return clampOffset(lines, lines, m.previewCapacity())
}

// clampOffset is scrollWindow's counterpart for a viewport with no cursor to
// keep in view, just a window that must not scroll past either end.
func clampOffset(offset, total, capacity int) int {
	if total <= capacity {
		return 0
	}
	return min(max(offset, 0), total-capacity)
}

// previewFrame is frame's counterpart for the preview: which of its furniture
// one rendered frame draws, and how many message lines are left over.
type previewFrame struct {
	header bool
	// headerLines is how tall the header box is for this session, since a
	// background agent's progress line makes it one taller.
	headerLines int
	spacer      bool
	compose     bool
	footer      bool
	lines       int
}

func (f previewFrame) chromeLines() int {
	lines := 0
	if f.header {
		lines += f.headerLines
	}
	if f.compose {
		lines += composeLines
	}
	for _, drawn := range []bool{f.spacer, f.footer} {
		if drawn {
			lines++
		}
	}
	return lines
}

// previewFrameFor surrenders furniture cheapest-first like frameFor and
// paletteFrameFor do, and for the same reason: a frame taller than the
// terminal loses its top lines to the renderer. One content line always
// survives.
func (m model) previewFrameFor() previewFrame {
	f := previewFrame{header: true, headerLines: m.previewHeaderHeight(), spacer: true, compose: m.composing, footer: true}
	height := cmp.Or(m.height, defaultHeight)

	// The compose box is surrendered last of all: it is the only line the owner
	// is actively typing into, so it outranks even the footer that names its
	// keys.
	for _, give := range []*bool{&f.header, &f.spacer, &f.footer, &f.compose} {
		if height-f.chromeLines() >= 1 {
			break
		}
		*give = false
	}

	f.lines = max(height-f.chromeLines(), 1)
	return f
}

func (m model) previewCapacity() int {
	return m.previewFrameFor().lines
}

func (m model) previewView() string {
	f := m.previewFrameFor()
	lines := m.previewLines()
	start := clampOffset(m.previewOffset, len(lines), f.lines)
	end := min(start+f.lines, len(lines))

	var out []string
	if f.header {
		out = append(out, m.previewHeaderBox()...)
	}

	var content []string
	switch {
	case m.previewLoad && len(lines) == 0:
		content = append(content, dimStyle.Render(gutter("loading…", m.width)))
	case len(lines) == 0:
		content = append(content, dimStyle.Render(gutter("no messages", m.width)))
	default:
		content = append(content, lines[start:end]...)
	}
	out = append(out, padTo(content, f.lines)...)

	if f.spacer {
		out = append(out, "")
	}
	if f.compose {
		out = append(out, m.composeLine())
	}
	if f.footer {
		out = append(out, m.previewFooterLine())
	}
	return strings.Join(out, "\n")
}

// previewFooterHint names the preview's own keys plus ctrl+j, which acts on
// previewTarget rather than the cursor's row the way browsing mode's does.
// composeFooterHint replaces it while the compose box is open, where enter and
// esc both mean something else.
const (
	// previewMessageCap is how many rendered lines one message may occupy
	// before the rest is held back behind a marker. Twelve leaves room for two
	// or three messages on an ordinary terminal, which is what makes the
	// preview an exchange rather than one wall of text.
	previewMessageCap = 12

	previewFooterHint = "↑/↓ ^u/^d scroll  ⏎ resume  i message  e expand  esc back  ^j jump"
	composeFooterHint = "⏎ send   esc cancel"
)

// previewFooterLine mirrors footerLine's status-over-hints priority: a jump or
// send error must survive on screen rather than be silently replaced by the
// hint text on the very next frame.
func (m model) previewFooterLine() string {
	switch {
	case m.status != "":
		return gutter(untrusted.Text(m.status), m.width)
	case m.composing:
		return dimStyle.Render(gutter(composeFooterHint, m.width))
	default:
		return dimStyle.Render(gutter(previewFooterHint, m.width))
	}
}

// previewHeaderHeight is what previewFrameFor counts against the terminal
// height, and what previewHeaderBox must then return exactly.
func (m model) previewHeaderHeight() int {
	if m.previewDetailLine() != "" {
		return previewHeaderDetailLines
	}
	return previewHeaderLines
}

// previewDetailLine is the background agent's own progress line, with its token
// count. Both are absent for an ordinary session, which is every session that
// was not started as a background agent.
func (m model) previewDetailLine() string {
	target := m.previewTarget

	var parts []string
	if target.JobState != "" {
		parts = append(parts, target.JobState)
	}
	if target.Detail != "" {
		parts = append(parts, target.Detail)
	}
	if target.Tokens > 0 {
		parts = append(parts, fmt.Sprintf("%s tokens", thousands(target.Tokens)))
	}
	return strings.Join(parts, " · ")
}

// previewHeaderBox always returns previewHeaderHeight lines, which is what
// previewFrameFor counted against the terminal height before this was called.
func (m model) previewHeaderBox() []string {
	width := tableWidth(m.width)
	if m.width > 0 {
		width = min(width, m.width)
	}

	var parts []string
	if dir := shortenHome(m.previewTarget.Cwd); dir != "" {
		parts = append(parts, dir)
	}
	if m.previewTarget.GitBranch != "" {
		parts = append(parts, m.previewTarget.GitBranch)
	}

	box := []string{
		dimStyle.Render(boxTop(m.previewTarget.Name, width)),
		boxRow(strings.Join(parts, " · "), width),
	}
	if detail := m.previewDetailLine(); detail != "" {
		box = append(box, boxRow(detail, width))
	}
	return append(box, dimStyle.Render(boxBottom(width)))
}

// previewLines flattens the loaded messages into the physical lines the
// preview scrolls over. It is recomputed from m.width every call rather than
// cached, the same discipline row() and columnHeaderLine() follow, so a
// resize can never leave a stale wrap on screen.
func (m model) previewLines() []string {
	limit := previewMessageCap
	if m.previewFull {
		limit = 0
	}
	return previewContentLines(m.previewMsgs, m.width, limit)
}

// previewRoleWidth and previewRoleGap size the "you"/"cc" column ahead of a
// message's text; gutterWidth is the row list's own left margin, reused so
// the preview's furniture lines up with the same idea of "where content
// starts" the rest of the picker uses.
const (
	previewRoleWidth = 3
	previewRoleGap   = 2

	// previewToolNameWidth columns a tool call's name so its argument starts
	// at the same cell regardless of the name's own length ("Bash", "Read",
	// "WebFetch"), the same left-justified-column idea agentWidth applies to
	// the row list's own tool-adjacent column.
	previewToolNameWidth = 8
)

// previewContentLines wraps prose to width rather than truncating it: a
// truncated message could never be "longer than the viewport" in the
// vertical sense the owner scrolls through, so the only way a long message
// stays reachable past its first screenful is for its overflow to become
// more lines, not to disappear behind an ellipsis. A tool call or its result
// is the opposite case — its whole point is a one-line summary — so those are
// clamped to width instead, the same as every other single-line furniture in
// the picker.
func previewContentLines(messages []session.Message, width, limit int) []string {
	indent := strings.Repeat(" ", gutterWidth+previewRoleWidth+previewRoleGap)
	wrapWidth := 0
	if width > 0 {
		wrapWidth = max(width-lipgloss.Width(indent), 1)
	}

	var lines []string
	for _, msg := range messages {
		var own []string
		parts := msg.Parts()
		showLabel := len(parts) > 0 && parts[0].Kind == session.ContentText
		label := strings.Repeat(" ", gutterWidth) + pad(roleLabel(msg.Role), previewRoleWidth) + strings.Repeat(" ", previewRoleGap)

		prefixFor := func() string {
			if showLabel {
				showLabel = false
				return label
			}
			return indent
		}

		for _, part := range parts {
			switch part.Kind {
			case session.ContentToolCall:
				line := clampLine(prefixFor()+toolCallLine(part.Tool, part.Arg), width)
				own = append(own, dimStyle.Render(line))
			case session.ContentToolResult:
				line := clampLine(prefixFor()+"‹ "+part.Result, width)
				own = append(own, dimStyle.Render(line))
			default:
				for _, chunk := range strings.Split(part.Text, "\n") {
					for _, physical := range wrapChunk(chunk, wrapWidth) {
						line := strings.TrimRight(clampLine(prefixFor()+physical, width), " ")
						own = append(own, line)
					}
				}
			}
		}
		lines = append(lines, capMessage(own, limit, indent, width)...)
	}
	return lines
}

// capMessage trims one message to limit rendered lines and adds a line saying
// how many it held back. One pasted brief or one screenful of curl examples
// otherwise fills the whole viewport, and the messages either side of it — the
// reason for opening the preview — scroll out of reach. The head is kept rather
// than the tail: a message is read from the top, and its opening line is what
// identifies it.
//
// The marker is an extra line rather than one of the limit, so the count it
// reports is simply what was dropped. Spending a content line on it would make
// one held-back line impossible to report — the message would have been within
// the limit already.
func capMessage(own []string, limit int, indent string, width int) []string {
	if limit <= 0 || len(own) <= limit {
		return own
	}

	held := len(own) - limit
	marker := fmt.Sprintf("%s… +%d more %s", indent, held, plural("line", held))
	return append(own[:limit], dimStyle.Render(strings.TrimRight(clampLine(marker, width), " ")))
}

func plural(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// toolCallLine renders a tool_use as "› <tool>   <arg>": the arrow marks it
// as machine action rather than something the owner said, and shortenHome
// gives a long absolute path the same chance to fit that the row list's own
// directory column already gets.
func toolCallLine(tool, arg string) string {
	return "› " + pad(tool, previewToolNameWidth) + shortenHome(arg)
}

// wrapChunk reflows text to width terminal cells rather than cutting it, so a
// paragraph of user prose or a pasted diff becomes more lines instead of a
// dead end. lipgloss wraps at cell boundaries, so a CJK or emoji chunk lands
// exactly at width the same way an ASCII one does.
func wrapChunk(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	return strings.Split(lipgloss.NewStyle().Width(width).Render(text), "\n")
}

func roleLabel(role string) string {
	if role == "user" {
		return "you"
	}
	return "cc"
}
