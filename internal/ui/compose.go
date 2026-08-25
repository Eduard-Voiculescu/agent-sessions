package ui

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// composeLines is how many lines the compose box draws: one, the input itself.
// The keys that act on it live in the preview's own footer, so the box costs
// the message area a single line.
const composeLines = 1

// sendResultMsg carries a finished send back into the loop, since the send
// shells out to osascript and could block on a human the way Jump can. text
// travels with it so a failure can hand the typed message back rather than
// discard it: the pane may have gone away between opening the preview and
// pressing enter, and retyping a paragraph is the wrong price for that.
type sendResultMsg struct {
	gen  int
	text string
	err  error
}

// startComposing opens the compose box over the previewed session. The target
// is already captured in previewTarget, so a tick that re-sorts the rows
// cannot move the message to a different session mid-typing.
func (m model) startComposing() model {
	m = m.composeToggled(true)
	m.composeText = ""
	m.status = ""
	return m
}

// composeToggled opens or closes the box and re-anchors a viewport that was
// following the tail. The box takes its line from the message area, so the
// bottom offset moves under an owner who never scrolled — without this,
// opening the box on a live session quietly stops it following the reply.
func (m model) composeToggled(open bool) model {
	following := m.previewOffset >= m.previewBottom()
	m.composing = open
	if following {
		m.previewOffset = m.previewBottom()
	}
	return m
}

func (m model) updateComposing(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m = m.composeToggled(false)
		m.composeText = ""
		return m, nil

	case tea.KeyEnter:
		return m.sendComposed()

	case tea.KeyBackspace:
		if m.composeText != "" {
			_, width := utf8.DecodeLastRuneInString(m.composeText)
			m.composeText = m.composeText[:len(m.composeText)-width]
		}
		return m, nil

	// KeySpace carries the space in Runes, so a message would otherwise arrive
	// as one unbroken word.
	case tea.KeyRunes, tea.KeySpace:
		m.composeText += printable(msg.Runes)
		return m, nil
	}

	return m, nil
}

// printable drops control runes. Send refuses them outright, so admitting one
// here would build a message that can only fail at the far end — and the
// keystrokes that produce them (ctrl+j, ctrl+u) are the preview's own bindings
// rather than text anyone meant to type.
func printable(runes []rune) string {
	var kept strings.Builder
	for _, r := range runes {
		if !unicode.IsControl(r) {
			kept.WriteRune(r)
		}
	}
	return kept.String()
}

// sendComposed splits on whether the session has a process to type into. A live
// one is typed into and the preview stays put, so the reply can be watched
// arriving. One with no process cannot be typed into at all: sending to it is
// resuming it with the message as its opening prompt, which replaces this
// program, so it leaves through the same exit any other resume does.
func (m model) sendComposed() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.composeText)
	if text == "" {
		return m, nil
	}

	target := m.previewTarget
	if !target.Live || target.PID <= 0 {
		m.composing = false
		m.composeText = ""
		m.status = ""
		m.choice = Choice{Session: target, Message: text}
		m.chosen = true
		m.done = true
		return m, tea.Quit
	}

	if m.send == nil {
		m.status = "send is unavailable"
		return m, nil
	}

	m = m.composeToggled(false)
	m.composeText = ""
	m.status = "sending…"

	send, pid, gen := m.send, target.PID, m.previewGen
	return m, func() tea.Msg {
		return sendResultMsg{gen: gen, text: text, err: send(context.Background(), pid, text)}
	}
}

// handleSendResult reports the outcome and, on success, re-reads the transcript
// so the sent line shows up without waiting out the tick. A failure reopens the
// compose box holding the message, but only if the owner is still on the
// preview it was typed in: reopening it over a different session would aim the
// retry at the wrong pane.
func (m model) handleSendResult(msg sendResultMsg) (tea.Model, tea.Cmd) {
	if msg.err == nil {
		m.status = "sent"
		if msg.gen == m.previewGen && m.previewing {
			return m, m.refreshPreviewCmd()
		}
		return m, nil
	}

	m.status = msg.err.Error()
	// A box already open holds something newer: the owner started retyping while
	// the send was in flight, and overwriting that with the failed message would
	// lose the very thing this branch exists to protect.
	if msg.gen == m.previewGen && m.previewing && !m.composing {
		m = m.composeToggled(true)
		m.composeText = msg.text
	}
	return m, nil
}

// composePrompt names where enter will send, because the two destinations
// differ in kind and the difference is not recoverable: typing into a live pane
// leaves the owner here, while resuming replaces this program with the agent.
func (m model) composePrompt() string {
	if m.previewTarget.Live && m.previewTarget.PID > 0 {
		return "send › "
	}
	return "resume with › "
}

// composeLine renders the input with a caret at the end. A message longer than
// the line scrolls its head off rather than its tail: what someone is typing
// right now is the part that has to stay on screen.
func (m model) composeLine() string {
	prompt := m.composePrompt()
	text := m.composeText
	if m.width > 0 {
		text = tailCells(text, m.width-gutterWidth-lipgloss.Width(prompt)-1)
	}
	return gutter(prompt+text+"_", m.width)
}

// tailCells keeps the last width cells of value, walking back a rune at a time.
// A combining mark left stranded at the cut measures zero cells, so it rides
// along rather than pushing the line over the limit.
func tailCells(value string, width int) string {
	if width <= 0 {
		return ""
	}

	runes := []rune(value)
	cells := 0
	for i := len(runes) - 1; i >= 0; i-- {
		cells += lipgloss.Width(string(runes[i]))
		if cells > width {
			return string(runes[i+1:])
		}
	}
	return value
}
