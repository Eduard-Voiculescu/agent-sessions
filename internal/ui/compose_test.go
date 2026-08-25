package ui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

type sentMessage struct {
	pid  int
	text string
}

// composeConfig builds a picker whose Preview resolves synchronously and whose
// Send records what it was asked to do, so a test can drive the whole compose
// path without a terminal or an osascript.
func composeConfig(sent *[]sentMessage, sendErr error) Config {
	return Config{
		Sessions: testSessions(),
		Preview: func(session.Session) ([]session.Message, error) {
			return []session.Message{{Role: "user", Text: "earlier"}}, nil
		},
		Send: func(_ context.Context, pid int, text string) error {
			*sent = append(*sent, sentMessage{pid: pid, text: text})
			return sendErr
		},
	}
}

// openComposeOn opens the preview on a row, resolves the read, then opens the
// compose box and types text into it a key at a time, the way the owner does.
func openComposeOn(t *testing.T, cfg Config, cursor int, text string) (model, []tea.Cmd) {
	t.Helper()

	m := sized(t, newModel(cfg), 80, 24)
	m.cursor = cursor

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(cmd())
	loaded := next.(model)
	if loaded.previewLoad {
		t.Fatal("setup: the preview read never resolved")
	}

	next, _ = loaded.Update(keyMsg('i'))
	composing := next.(model)
	if !composing.composing {
		t.Fatal("setup: 'i' did not open the compose box")
	}

	var cmds []tea.Cmd
	for _, r := range text {
		msg := keyMsg(r)
		if r == ' ' {
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		}
		next, cmd := composing.Update(msg)
		composing = next.(model)
		cmds = append(cmds, cmd)
	}
	return composing, cmds
}

func TestComposeTypingBuildsTheMessageAndShowsItWithACaret(t *testing.T) {
	var sent []sentMessage
	m, _ := openComposeOn(t, composeConfig(&sent, nil), 0, "run the tests")

	if m.composeText != "run the tests" {
		t.Errorf("composeText = %q, want %q", m.composeText, "run the tests")
	}
	view := m.View()
	if !strings.Contains(view, "run the tests_") {
		t.Errorf("View() does not show the typed message with a caret:\n%s", view)
	}
	if !strings.Contains(view, "⏎ send") {
		t.Errorf("View() does not tell the owner enter sends:\n%s", view)
	}
}

// The two destinations are not interchangeable: typing into a live pane leaves
// the owner in the preview, while a session with no process is resumed with the
// message, which replaces this program. The prompt has to say which one enter
// is about to do.
func TestComposePromptNamesTheDestination(t *testing.T) {
	var sent []sentMessage

	live, _ := openComposeOn(t, composeConfig(&sent, nil), 0, "hi")
	if got := live.View(); !strings.Contains(got, "send › ") {
		t.Errorf("a live session's compose box does not say it sends:\n%s", got)
	}

	history, _ := openComposeOn(t, composeConfig(&sent, nil), 1, "hi")
	if got := history.View(); !strings.Contains(got, "resume with › ") {
		t.Errorf("a history session's compose box does not say it resumes:\n%s", got)
	}
}

// A live session is typed into, and the owner stays put: the whole reason to
// send rather than resume is to watch the reply land in the preview already on
// screen.
func TestComposeEnterSendsToTheLivePaneAndStaysInThePreview(t *testing.T) {
	var sent []sentMessage
	m, _ := openComposeOn(t, composeConfig(&sent, nil), 0, "run the tests")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	after := next.(model)

	if cmd == nil {
		t.Fatal("Update() returned no Cmd, want the send handed to one")
	}
	if len(sent) != 0 {
		t.Fatalf("Send ran inside Update, which blocks the event loop: %+v", sent)
	}
	if _, ok := cmd().(sendResultMsg); !ok {
		t.Fatal("the Cmd did not return a sendResultMsg")
	}
	if len(sent) != 1 || sent[0].pid != 7 || sent[0].text != "run the tests" {
		t.Fatalf("Send got %+v, want one send of %q to pid 7", sent, "run the tests")
	}

	if after.done || after.chosen {
		t.Error("sending to a live pane quit the picker, want it to stay in the preview")
	}
	if !after.previewing {
		t.Error("model.previewing = false, want the preview still open after a send")
	}
	if after.composing || after.composeText != "" {
		t.Errorf("the compose box survived the send: composing=%v text=%q", after.composing, after.composeText)
	}
}

// A session with no process cannot be typed into, so sending to it is resuming
// it with the message as its opening prompt. That replaces this program, so the
// message has to leave through Choice rather than through Send.
func TestComposeEnterOnAHistoryRowResumesWithTheMessage(t *testing.T) {
	var sent []sentMessage
	m, _ := openComposeOn(t, composeConfig(&sent, nil), 1, "pick this up again")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	after := next.(model)

	if len(sent) != 0 {
		t.Errorf("Send was called for a session with no process: %+v", sent)
	}
	if !after.chosen || !after.done {
		t.Fatal("model.chosen and .done, want both true so the resume can run")
	}
	if after.choice.Session.ID != "hist" {
		t.Errorf("choice.Session.ID = %q, want %q", after.choice.Session.ID, "hist")
	}
	if after.choice.Message != "pick this up again" {
		t.Errorf("choice.Message = %q, want the composed message", after.choice.Message)
	}
	if after.choice.Fork {
		t.Error("choice.Fork = true, want a message to resume the session rather than fork it")
	}
}

func TestComposeEscapeCancelsWithoutSending(t *testing.T) {
	var sent []sentMessage
	m, _ := openComposeOn(t, composeConfig(&sent, nil), 0, "never mind")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	after := next.(model)

	if cmd != nil {
		t.Error("escaping the compose box returned a Cmd, want nothing dispatched")
	}
	if len(sent) != 0 {
		t.Errorf("Send was called after esc: %+v", sent)
	}
	if after.composing || after.composeText != "" {
		t.Errorf("compose state survived esc: composing=%v text=%q", after.composing, after.composeText)
	}
	if !after.previewing {
		t.Error("esc from the compose box left the preview too, want it to return to the messages")
	}
}

// Enter is the likeliest keystroke on an empty box — it is what opened the
// preview in the first place — and an empty send would either type a bare
// newline into someone's live agent or resume a session with an empty prompt.
func TestComposeEnterOnAnEmptyMessageDoesNothing(t *testing.T) {
	for _, typed := range []string{"", "   "} {
		t.Run("typed="+typed, func(t *testing.T) {
			var sent []sentMessage
			m, _ := openComposeOn(t, composeConfig(&sent, nil), 0, typed)

			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			after := next.(model)

			if cmd != nil {
				t.Error("an empty message dispatched a Cmd")
			}
			if len(sent) != 0 {
				t.Errorf("an empty message was sent: %+v", sent)
			}
			if after.done || after.chosen {
				t.Error("an empty message quit the picker")
			}
			if !after.composing {
				t.Error("an empty message closed the compose box, want it left open to type into")
			}
		})
	}
}

func TestComposeBackspaceDeletesWholeRunes(t *testing.T) {
	var sent []sentMessage
	m, _ := openComposeOn(t, composeConfig(&sent, nil), 0, "héllo")

	after := press(t, m, tea.KeyMsg{Type: tea.KeyBackspace}, 4)
	if after.composeText != "h" {
		t.Errorf("composeText = %q, want %q; backspace must delete whole runes", after.composeText, "h")
	}

	empty := press(t, after, tea.KeyMsg{Type: tea.KeyBackspace}, 3)
	if empty.composeText != "" {
		t.Errorf("composeText = %q, want empty after backspacing past the start", empty.composeText)
	}
}

// termjump.Send refuses control characters, so one admitted here builds a
// message that can only fail at the far end.
func TestComposeNeverAdmitsAControlRune(t *testing.T) {
	var sent []sentMessage
	m, _ := openComposeOn(t, composeConfig(&sent, nil), 0, "ok")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a', '\x1b', '\n', 'b'}})
	after := next.(model)

	if after.composeText != "okab" {
		t.Errorf("composeText = %q, want %q with the control runes dropped", after.composeText, "okab")
	}
}

// A pane can go away between opening the preview and pressing enter. Losing a
// typed paragraph to that is the wrong price, so the box reopens holding it and
// the reason is on screen.
func TestComposeSendFailureReopensTheBoxWithTheMessage(t *testing.T) {
	var sent []sentMessage
	m, _ := openComposeOn(t, composeConfig(&sent, errors.New("no iTerm2 pane found for pid 7")), 0, "run the tests")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(cmd())
	after := next.(model)

	if !after.composing {
		t.Error("model.composing = false, want the box reopened after a failed send")
	}
	if after.composeText != "run the tests" {
		t.Errorf("composeText = %q, want the message handed back", after.composeText)
	}
	view := after.View()
	if !strings.Contains(view, "no iTerm2 pane found") {
		t.Errorf("View() does not show why the send failed:\n%s", view)
	}
	if !after.previewing || after.done {
		t.Error("a failed send left the preview or quit, want the picker still running")
	}
}

// A send that landed while the owner has moved on to a different session must
// not reopen the box there: the retry would aim at the wrong pane.
func TestComposeSendFailureCannotReopenOverADifferentPreview(t *testing.T) {
	var sent []sentMessage
	m, _ := openComposeOn(t, composeConfig(&sent, errors.New("pane is gone")), 0, "hello")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	sending := next.(model)

	next, _ = sending.Update(tea.KeyMsg{Type: tea.KeyEsc})
	back := next.(model)
	back.cursor = 1
	next, openCmd := back.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(openCmd())
	elsewhere := next.(model)

	next, _ = elsewhere.Update(cmd())
	after := next.(model)

	if after.composing {
		t.Error("a stale send failure reopened the compose box over a different session")
	}
	if after.composeText != "" {
		t.Errorf("composeText = %q, want it not carried onto another session", after.composeText)
	}
	if !strings.Contains(after.View(), "pane is gone") {
		t.Errorf("the failure was swallowed entirely:\n%s", after.View())
	}
}

// The message only appears in the transcript once the agent has read it, so a
// successful send re-reads rather than waiting out the tick.
func TestComposeSuccessfulSendRefreshesTheTranscript(t *testing.T) {
	var reads atomic.Int32
	var sent []sentMessage
	cfg := composeConfig(&sent, nil)
	cfg.Preview = func(session.Session) ([]session.Message, error) {
		reads.Add(1)
		return []session.Message{{Role: "user", Text: "earlier"}}, nil
	}

	m, _ := openComposeOn(t, cfg, 0, "run the tests")
	before := reads.Load()

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, follow := next.(model).Update(cmd())
	after := next.(model)

	if after.status != "sent" {
		t.Errorf("status = %q, want it to confirm the send", after.status)
	}
	if follow == nil {
		t.Fatal("a successful send returned no Cmd, want the transcript re-read")
	}
	msg, ok := follow().(previewResultMsg)
	if !ok {
		t.Fatalf("the follow-up Cmd returned %T, want a previewResultMsg", follow())
	}
	if !msg.refresh {
		t.Error("the re-read is not marked as a refresh, so it would re-anchor a scrolled preview")
	}
	if reads.Load() <= before {
		t.Error("the transcript was not re-read after the send")
	}
}

// A refresh follows the tail only for an owner already sitting on it. Yanking
// someone back to the bottom once a second while they read something further up
// makes the preview unusable on a busy session.
func TestPreviewRefreshFollowsTheTailOnlyWhenAlreadyAtTheBottom(t *testing.T) {
	grown := append(previewTestMessages(40), session.Message{Role: "assistant", Text: "brand new line"})

	atBottom := openLoadedPreview(t, previewTestMessages(40), 80, 20)
	if atBottom.previewOffset != atBottom.previewBottom() {
		t.Fatalf("setup: a freshly loaded preview is not at the bottom")
	}
	next, _ := atBottom.Update(previewResultMsg{gen: atBottom.previewGen, messages: grown, refresh: true})
	followed := next.(model)
	if followed.previewOffset != followed.previewBottom() {
		t.Errorf("previewOffset = %d, want the bottom %d: a refresh must carry a tailing reader along", followed.previewOffset, followed.previewBottom())
	}
	if !strings.Contains(followed.View(), "brand new line") {
		t.Errorf("the new message never reached the screen:\n%s", followed.View())
	}

	scrolled := press(t, atBottom, tea.KeyMsg{Type: tea.KeyUp}, 6)
	held := scrolled.previewOffset
	if held == scrolled.previewBottom() {
		t.Fatalf("setup: scrolling up left the offset at the bottom")
	}
	next, _ = scrolled.Update(previewResultMsg{gen: scrolled.previewGen, messages: grown, refresh: true})
	if got := next.(model).previewOffset; got != held {
		t.Errorf("previewOffset = %d, want %d held: a refresh must not yank a reader to the bottom", got, held)
	}
}

// A transcript is appended to while it is being read, so a torn read is normal
// traffic. Closing the preview over one would eject the owner mid-conversation;
// only a first read, which has nothing to show, falls back to the list.
func TestPreviewRefreshFailureKeepsThePreviewOpen(t *testing.T) {
	m := openLoadedPreview(t, previewTestMessages(6), 80, 20)
	before := len(m.previewMsgs)

	next, _ := m.Update(previewResultMsg{gen: m.previewGen, err: errors.New("unexpected end of JSON input"), refresh: true})
	after := next.(model)

	if !after.previewing {
		t.Error("a failed refresh closed the preview")
	}
	if len(after.previewMsgs) != before {
		t.Errorf("previewMsgs went from %d to %d, want the last good read kept", before, len(after.previewMsgs))
	}
	if !strings.Contains(after.View(), "unexpected end of JSON input") {
		t.Errorf("the failed refresh was silent:\n%s", after.View())
	}
}

// Only a live session's transcript can grow. Re-reading a history session's
// once a second spends disk on a file that cannot change.
func TestPreviewTickRefreshesLiveSessionsOnly(t *testing.T) {
	for _, tt := range []struct {
		name       string
		cursor     int
		wantReread bool
	}{
		{"live", 0, true},
		{"history", 1, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var reads atomic.Int32
			m := sized(t, newModel(Config{
				Sessions: testSessions(),
				Preview: func(session.Session) ([]session.Message, error) {
					reads.Add(1)
					return previewTestMessages(4), nil
				},
			}), 80, 24)
			m.cursor = tt.cursor

			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			next, _ = next.(model).Update(cmd())
			loaded := next.(model)
			before := reads.Load()

			if got := loaded.refreshPreviewCmd(); (got != nil) != tt.wantReread {
				t.Fatalf("refreshPreviewCmd() non-nil = %v, want %v", got != nil, tt.wantReread)
			}
			if !tt.wantReread {
				return
			}
			if _, ok := loaded.refreshPreviewCmd()().(previewResultMsg); !ok {
				t.Error("the refresh Cmd did not return a previewResultMsg")
			}
			if reads.Load() <= before {
				t.Error("the refresh never read the transcript")
			}
		})
	}
}

// A read slower than the one-second tick would otherwise stack up behind
// itself, one goroutine per tick, all reading the same file.
func TestPreviewRefreshDoesNotStackBehindAReadInFlight(t *testing.T) {
	release := make(chan struct{})
	m := sized(t, newModel(Config{
		Sessions: testSessions(),
		Preview: func(session.Session) ([]session.Message, error) {
			<-release
			return nil, nil
		},
	}), 80, 24)
	m.cursor = 0

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pending := next.(model)
	if !pending.previewLoad {
		t.Fatal("setup: the first read is not marked in flight")
	}
	if got := pending.refreshPreviewCmd(); got != nil {
		t.Error("refreshPreviewCmd() dispatched a second read while one was in flight")
	}

	close(release)
	if cmd != nil {
		cmd()
	}
}

// A refresh must not blank the messages already on screen behind a "loading…"
// line: the indicator is for a preview with nothing to show yet.
func TestPreviewRefreshDoesNotReplaceMessagesWithLoading(t *testing.T) {
	m := openLoadedPreview(t, previewTestMessages(6), 80, 20)
	m.previewLoad = true

	if view := m.View(); strings.Contains(view, "loading") {
		t.Errorf("View() hid loaded messages behind a loading indicator during a refresh:\n%s", view)
	}
}

// The compose box costs the message area a line, so a frame that forgets to
// count it overflows the terminal — and bubbletea drops the overflow off the
// top, which is where the header and the messages are.
func TestComposeViewFitsTheTerminalHeight(t *testing.T) {
	for _, height := range []int{2, 3, 6, 10, 24} {
		t.Run(strings.Repeat("h", 1)+string(rune('0'+height%10)), func(t *testing.T) {
			var sent []sentMessage
			cfg := composeConfig(&sent, nil)
			cfg.Preview = func(session.Session) ([]session.Message, error) {
				return previewTestMessages(60), nil
			}

			m := sized(t, newModel(cfg), 80, height)
			m.cursor = 0
			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			next, _ = next.(model).Update(cmd())
			next, _ = next.(model).Update(keyMsg('i'))
			composing := next.(model)

			lines := strings.Split(composing.View(), "\n")
			if len(lines) > height {
				t.Errorf("View() rendered %d lines in a %d-line terminal:\n%s", len(lines), height, composing.View())
			}
			for _, line := range lines {
				if got := lipgloss.Width(line); got > 80 {
					t.Errorf("line is %d cells wide in an 80-column terminal: %q", got, line)
				}
			}
		})
	}
}

// The box is the one line being typed into, so it outranks the footer that
// merely names its keys: a terminal too short for both keeps the box.
func TestComposeSurvivesAfterTheFooterIsGivenUp(t *testing.T) {
	var sent []sentMessage
	cfg := composeConfig(&sent, nil)

	m := sized(t, newModel(cfg), 80, 2)
	m.cursor = 0
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(cmd())
	next, _ = next.(model).Update(keyMsg('i'))
	composing := next.(model)

	f := composing.previewFrameFor()
	if !f.compose {
		t.Error("previewFrameFor() gave up the compose box while the footer or header could still go")
	}
	if f.footer {
		t.Error("previewFrameFor() kept the footer at a height too short for the box and the messages")
	}
}

// A message longer than the line scrolls its head off, not its tail: what is
// being typed right now is the part that has to stay visible.
func TestComposeLineKeepsTheCaretVisibleOnALongMessage(t *testing.T) {
	var sent []sentMessage
	m, _ := openComposeOn(t, composeConfig(&sent, nil), 0, "")
	m.width = 40
	m.composeText = strings.Repeat("abcdefghij", 12) + "END"

	line := m.composeLine()
	if got := lipgloss.Width(line); got > 40 {
		t.Errorf("compose line is %d cells wide in a 40-column terminal: %q", got, line)
	}
	if !strings.Contains(line, "END_") {
		t.Errorf("compose line lost the caret end of the message: %q", line)
	}
}

func TestTailCellsKeepsTheEndWithinTheWidth(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
		width int
		want  string
	}{
		{"fits", "abc", 10, "abc"},
		{"exact", "abc", 3, "abc"},
		{"trims the head", "abcdef", 3, "def"},
		{"no room", "abcdef", 0, ""},
		{"negative", "abcdef", -2, ""},
		{"wide runes", "日本語です", 4, "です"},
		// A double-width rune that straddles the limit is dropped whole rather
		// than half-drawn: 語です is six cells, and five is not enough for it.
		{"wide rune straddling the limit", "日本語です", 5, "です"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := tailCells(tt.value, tt.width)
			if got != tt.want {
				t.Errorf("tailCells(%q, %d) = %q, want %q", tt.value, tt.width, got, tt.want)
			}
			if tt.width > 0 && lipgloss.Width(got) > tt.width {
				t.Errorf("tailCells(%q, %d) = %q, which is %d cells", tt.value, tt.width, got, lipgloss.Width(got))
			}
		})
	}
}

// ctrl+c is intercepted ahead of every mode dispatch, and the compose box takes
// raw runes: a TUI that keeps running on ctrl+c reads as hung.
func TestComposeCtrlCStillQuits(t *testing.T) {
	var sent []sentMessage
	m, _ := openComposeOn(t, composeConfig(&sent, nil), 0, "half a thought")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c returned no Cmd, want the program quit")
	}
	if !next.(model).done {
		t.Error("model.done = false after ctrl+c inside the compose box")
	}
}

// ctrl+p is intercepted ahead of the mode dispatch too, so it must be told to
// stand down here the way it already is for the preview: a palette opening over
// a half-typed message loses it.
func TestComposeCtrlPDoesNotOpenThePalette(t *testing.T) {
	var sent []sentMessage
	cfg := composeConfig(&sent, nil)
	cfg.Commands = func() []Command { return nil }

	m, _ := openComposeOn(t, cfg, 0, "half a thought")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	after := next.(model)

	if after.palette {
		t.Error("ctrl+p opened the palette over the compose box")
	}
	if after.composeText != "half a thought" {
		t.Errorf("composeText = %q, want the message untouched", after.composeText)
	}
}

// The send is aimed at the session the preview was opened for, captured by
// value. A tick re-sorting the rows under the cursor mid-typing must not
// redirect the message to whichever session slid into the slot.
func TestComposeSendCannotBeRetargetedByATick(t *testing.T) {
	var sent []sentMessage
	cfg := composeConfig(&sent, nil)
	cfg.Refresh = func() []session.Session {
		return []session.Session{{Agent: "claude", ID: "other", PID: 999, Live: true, Status: "running"}}
	}
	cfg.Filter = func(s []session.Session) []session.Session { return s }

	m, _ := openComposeOn(t, cfg, 0, "run the tests")

	next, _ := m.Update(tickMsg{})
	ticked := next.(model)
	ticked.cursor = 0

	next, cmd := ticked.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Update() returned no Cmd, want the send handed to one")
	}
	cmd()

	if len(sent) != 1 || sent[0].pid != 7 {
		t.Fatalf("Send got %+v, want one send to pid 7, the previewed session", sent)
	}
	if next.(model).previewTarget.ID != "live" {
		t.Errorf("previewTarget.ID = %q, want it frozen at %q", next.(model).previewTarget.ID, "live")
	}
}

// Nothing to send to and nothing wired to send with are different failures, and
// neither may look like a message that went through.
func TestComposeReportsAnUnwiredSend(t *testing.T) {
	m := sized(t, newModel(Config{
		Sessions: testSessions(),
		Preview: func(session.Session) ([]session.Message, error) {
			return []session.Message{{Role: "user", Text: "earlier"}}, nil
		},
	}), 80, 24)
	m.cursor = 0

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(cmd())
	next, _ = next.(model).Update(keyMsg('i'))
	next, _ = next.(model).Update(keyMsg('x'))

	next, sendCmd := next.(model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	after := next.(model)

	if sendCmd != nil {
		t.Error("an unwired send dispatched a Cmd")
	}
	if !strings.Contains(after.View(), "send is unavailable") {
		t.Errorf("View() does not say the send is unavailable:\n%s", after.View())
	}
	if after.done {
		t.Error("an unwired send quit the picker")
	}
}

// The box takes its line from the message area. An owner sitting on the tail of
// a live session must stay on it: without re-anchoring, opening the box slides
// the bottom one line away and the preview quietly stops following the reply.
func TestComposeOpeningTheBoxKeepsAFollowingPreviewOnTheTail(t *testing.T) {
	var sent []sentMessage
	cfg := composeConfig(&sent, nil)
	cfg.Preview = func(session.Session) ([]session.Message, error) { return previewTestMessages(60), nil }

	m := sized(t, newModel(cfg), 80, 20)
	m.cursor = 0
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(cmd())
	loaded := next.(model)
	if loaded.previewOffset != loaded.previewBottom() {
		t.Fatalf("setup: a freshly loaded preview is not on the tail")
	}
	newest := loaded.previewMsgs[len(loaded.previewMsgs)-1].Text

	next, _ = loaded.Update(keyMsg('i'))
	composing := next.(model)

	if composing.previewOffset != composing.previewBottom() {
		t.Errorf("previewOffset = %d, want the bottom %d after the box took a line", composing.previewOffset, composing.previewBottom())
	}
	if !strings.Contains(composing.View(), newest) {
		t.Errorf("the newest message fell off screen when the box opened:\n%s", composing.View())
	}
}

// An owner who scrolled up to read something keeps their place: the box opening
// is not a reason to move them.
func TestComposeOpeningTheBoxLeavesAScrolledPreviewAlone(t *testing.T) {
	var sent []sentMessage
	cfg := composeConfig(&sent, nil)
	cfg.Preview = func(session.Session) ([]session.Message, error) { return previewTestMessages(60), nil }

	m := sized(t, newModel(cfg), 80, 20)
	m.cursor = 0
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(cmd())

	scrolled := press(t, next.(model), tea.KeyMsg{Type: tea.KeyUp}, 8)
	held := scrolled.previewOffset

	next, _ = scrolled.Update(keyMsg('i'))
	if got := next.(model).previewOffset; got != held {
		t.Errorf("previewOffset = %d, want %d held when the box opened", got, held)
	}
}

// A confirmation stays up until the owner does something, then gets out of the
// way: "sent" parked in the footer for the rest of the session costs the hint
// line and stops telling the truth about the next send.
func TestPreviewStatusClearsOnTheNextKey(t *testing.T) {
	var sent []sentMessage
	m, _ := openComposeOn(t, composeConfig(&sent, nil), 0, "run the tests")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(cmd())
	confirmed := next.(model)
	if !strings.Contains(confirmed.View(), "sent") {
		t.Fatalf("setup: the send was not confirmed:\n%s", confirmed.View())
	}

	next, _ = confirmed.Update(tea.KeyMsg{Type: tea.KeyUp})
	after := next.(model)
	if after.status != "" {
		t.Errorf("status = %q, want it cleared by the next keystroke", after.status)
	}
	if !strings.Contains(after.View(), previewFooterHint) {
		t.Errorf("View() never got its hint line back:\n%s", after.View())
	}
}

// A failure that arrives after the owner has started retyping must not
// overwrite what they are in the middle of.
func TestComposeSendFailureDoesNotClobberARetypedMessage(t *testing.T) {
	var sent []sentMessage
	m, _ := openComposeOn(t, composeConfig(&sent, errors.New("pane is gone")), 0, "first attempt")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	sending := next.(model)

	next, _ = sending.Update(keyMsg('i'))
	retyping := typeInto(next.(model), "second attempt")

	next, _ = retyping.Update(cmd())
	after := next.(model)

	if after.composeText != "second attempt" {
		t.Errorf("composeText = %q, want the retyped message kept", after.composeText)
	}
	if !strings.Contains(after.View(), "pane is gone") {
		t.Errorf("the failure was swallowed:\n%s", after.View())
	}
}

func typeInto(m model, text string) model {
	for _, r := range text {
		msg := keyMsg(r)
		if r == ' ' {
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		}
		next, _ := m.Update(msg)
		m = next.(model)
	}
	return m
}
