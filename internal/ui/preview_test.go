package ui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

func previewTestMessages(n int) []session.Message {
	messages := make([]session.Message, n)
	for i := range messages {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		messages[i] = session.Message{Role: role, Text: fmt.Sprintf("message %02d", i)}
	}
	return messages
}

// openLoadedPreview sizes the model, opens the preview on the first row and
// resolves the (synchronous, in these tests) Preview call, so the caller
// starts from a preview already showing messages rather than "loading…".
func openLoadedPreview(t *testing.T, messages []session.Message, width, height int) model {
	t.Helper()
	m := sized(t, newModel(Config{Sessions: testSessions(), Preview: func(session.Session) ([]session.Message, error) {
		return messages, nil
	}}), width, height)
	m.cursor = 0

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(cmd())
	return next.(model)
}

// The transcript read shells out to nothing, but it is still disk I/O on a
// file that can run to tens of megabytes; running it inside Update would
// freeze rendering, the tick and ctrl+c for as long as the read took, the
// same failure mode jump was found in and fixed by moving to a tea.Cmd.
func TestOpenPreviewDoesNotReadTheTranscriptInsideUpdate(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	m := newModel(Config{Sessions: testSessions(), Preview: func(session.Session) ([]session.Message, error) {
		close(started)
		<-release
		return nil, nil
	}})
	m.cursor = 0

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Update() returned no Cmd, want the transcript read handed to one")
	}
	select {
	case <-started:
		t.Fatal("Preview ran inside Update, which blocks the event loop until it returns")
	default:
	}

	if _, quit := next.(model).Update(tea.KeyMsg{Type: tea.KeyCtrlC}); quit == nil {
		t.Error("ctrl+c was ignored while a preview read was outstanding")
	}

	results := make(chan tea.Msg, 1)
	go func() { results <- cmd() }()
	<-started
	close(release)
	if msg, ok := (<-results).(previewResultMsg); !ok {
		t.Errorf("cmd() returned %T, want a previewResultMsg", msg)
	}
}

func TestPreviewViewShowsLoadingBeforeTheResultArrives(t *testing.T) {
	release := make(chan struct{})
	m := sized(t, newModel(Config{Sessions: testSessions(), Preview: func(session.Session) ([]session.Message, error) {
		<-release
		return nil, nil
	}}), 80, 24)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)
	if !strings.Contains(got.View(), "loading") {
		t.Errorf("View() does not show a loading indicator before the result arrives:\n%s", got.View())
	}

	close(release)
	if cmd != nil {
		cmd()
	}
}

// A failure has to leave a screen that still works, not a broken preview
// parked on nothing to scroll: the picker returns to the list, the same
// shape a failed jump already lands in.
func TestPreviewErrorLandsInTheFooterAndPickerSurvives(t *testing.T) {
	m := newModel(Config{Sessions: testSessions(), Preview: func(session.Session) ([]session.Message, error) {
		return nil, errors.New("opening transcript live.jsonl: permission denied")
	}})
	m.cursor = 0

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, followUp := next.(model).Update(cmd())
	result := next.(model)

	if !strings.Contains(result.View(), "permission denied") {
		t.Errorf("View() does not surface the preview error:\n%s", result.View())
	}
	if result.previewing {
		t.Error("model.previewing = true, want a failed preview to return to the list")
	}
	if result.done {
		t.Error("model.done = true, want a failed preview to leave the picker running")
	}
	if followUp != nil {
		t.Error("handling the preview result returned a non-nil Cmd, want nil so a failed preview cannot quit the program")
	}
}

func TestPreviewEnterResumesTheSessionThePreviewWasOpenedFor(t *testing.T) {
	m := newModel(Config{Sessions: testSessions(), Preview: func(session.Session) ([]session.Message, error) {
		return []session.Message{{Role: "user", Text: "hello"}}, nil
	}})
	m.cursor = 1 // the history row

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(cmd())
	previewing := next.(model)
	if previewing.previewLoad {
		t.Fatal("model.previewLoad = true, want the result already applied")
	}

	next, _ = previewing.Update(tea.KeyMsg{Type: tea.KeyEnter})
	resumed := next.(model)

	if !resumed.chosen || !resumed.done {
		t.Fatal("model.chosen and .done, want both true after the second Enter")
	}
	if resumed.choice.Session.ID != "hist" {
		t.Errorf("choice.Session.ID = %q, want %q", resumed.choice.Session.ID, "hist")
	}
	if resumed.choice.Fork {
		t.Error("choice.Fork = true, want false")
	}
}

// A second, later preview must win over a slow first one: opening a preview
// bumps previewGen, so a read started under the old generation cannot land
// on state that has since moved on to a different session.
func TestPreviewStaleResultCannotOverwriteANewerPreview(t *testing.T) {
	release := make(chan struct{})
	m := newModel(Config{Sessions: testSessions(), Preview: func(s session.Session) ([]session.Message, error) {
		if s.ID == "live" {
			<-release
			return []session.Message{{Role: "user", Text: "stale"}}, nil
		}
		return []session.Message{{Role: "user", Text: "fresh"}}, nil
	}})
	m.cursor = 0 // "live"

	next, staleCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	opened := next.(model)

	next, _ = opened.Update(tea.KeyMsg{Type: tea.KeyEsc})
	back := next.(model)
	back.cursor = 1 // "hist"

	next, freshCmd := back.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(freshCmd())
	current := next.(model)
	if len(current.previewMsgs) != 1 || current.previewMsgs[0].Text != "fresh" {
		t.Fatalf("previewMsgs = %+v, want the fresh preview's result", current.previewMsgs)
	}

	staleResult := make(chan tea.Msg, 1)
	go func() { staleResult <- staleCmd() }()
	close(release)
	next, _ = current.Update(<-staleResult)
	after := next.(model)

	if len(after.previewMsgs) != 1 || after.previewMsgs[0].Text != "fresh" {
		t.Errorf("a stale preview result overwrote the current one: %+v", after.previewMsgs)
	}
}

func TestPreviewScrollMovesWithinBoundsAndCannotPassEitherEnd(t *testing.T) {
	m := openLoadedPreview(t, previewTestMessages(40), 80, 20)
	total := len(m.previewLines())
	capacity := m.previewCapacity()
	if total <= capacity {
		t.Fatalf("fixture does not exceed the viewport: %d lines, capacity %d", total, capacity)
	}
	bottom := total - capacity
	if m.previewOffset != bottom {
		t.Fatalf("previewOffset = %d, want %d (bottom-anchored once loaded)", m.previewOffset, bottom)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := next.(model).previewOffset; got != bottom {
		t.Errorf("previewOffset = %d after scrolling past the bottom, want it clamped at %d", got, bottom)
	}

	up := press(t, m, tea.KeyMsg{Type: tea.KeyUp}, total)
	if up.previewOffset != 0 {
		t.Errorf("previewOffset = %d, want 0 clamped at the top", up.previewOffset)
	}

	next, _ = up.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := next.(model).previewOffset; got != 0 {
		t.Errorf("previewOffset = %d after scrolling past the top, want it clamped at 0", got)
	}

	down := press(t, up, tea.KeyMsg{Type: tea.KeyDown}, total)
	if down.previewOffset != bottom {
		t.Errorf("previewOffset = %d, want %d back at the bottom", down.previewOffset, bottom)
	}
}

func TestPreviewHalfPageKeysMoveWithinBounds(t *testing.T) {
	m := openLoadedPreview(t, previewTestMessages(60), 80, 20)
	half := m.previewHalfPage()
	bottom := m.previewOffset

	up := press(t, m, tea.KeyMsg{Type: tea.KeyCtrlU}, 1)
	if got := up.previewOffset; got != bottom-half {
		t.Errorf("previewOffset = %d, want %d after ctrl+u", got, bottom-half)
	}

	top := press(t, up, tea.KeyMsg{Type: tea.KeyCtrlU}, 20)
	if top.previewOffset != 0 {
		t.Errorf("previewOffset = %d, want 0; ctrl+u must not scroll above the top", top.previewOffset)
	}

	down := press(t, top, tea.KeyMsg{Type: tea.KeyCtrlD}, 20)
	if down.previewOffset != bottom {
		t.Errorf("previewOffset = %d, want %d; ctrl+d must not scroll past the bottom", down.previewOffset, bottom)
	}
}

// The capacity accounting regression this project already shipped once (a
// frame taller than the terminal loses lines off the top) is checked the same
// way the row list and palette check it: the actual rendered line count
// against the actual terminal size, never a hand-derived formula that could
// drift from previewCapacity the same way the bug did.
func TestPreviewViewRendersExactlyItsCapacityWhenThereIsMoreContentThanFits(t *testing.T) {
	m := openLoadedPreview(t, previewTestMessages(60), 80, 20)

	// Matched on the fixture's own numbered form, not the bare word: the
	// footer's hint text names the compose key as "i message", and a looser
	// match counts that line as content.
	fixture := regexp.MustCompile(`message \d\d`)
	rendered := 0
	for _, line := range strings.Split(m.View(), "\n") {
		if fixture.MatchString(line) {
			rendered++
		}
	}
	if rendered != m.previewCapacity() {
		t.Errorf("View() rendered %d content lines, want %d (previewCapacity)", rendered, m.previewCapacity())
	}
}

func TestPreviewViewDropsTheHeaderBeforeDroppingContent(t *testing.T) {
	m := openLoadedPreview(t, previewTestMessages(10), 80, 4)

	view := m.View()
	if strings.Contains(view, "╭") {
		t.Errorf("View() drew the header box at a height too short to afford it:\n%s", view)
	}
	if lines := len(strings.Split(view, "\n")); lines > 4 {
		t.Errorf("View() rendered %d lines, want at most 4:\n%s", lines, view)
	}
}

// The frame invariant, swept: no line wider than the terminal, no frame
// taller than it, including for messages carrying CJK and emoji, which a
// rune-based width count would under-count and let overflow the frame.
func TestPreviewViewFitsEveryTerminalSize(t *testing.T) {
	messages := previewTestMessages(30)
	messages = append(messages,
		session.Message{Role: "user", Text: "日本語のセッション名です " + strings.Repeat("長い文章です。", 20)},
		session.Message{Role: "assistant", Text: "🚀🚀🚀 " + strings.Repeat("emoji galore 🚀 ", 20)},
		session.Message{Role: "assistant", Content: []session.Content{
			{Kind: session.ContentToolCall, Tool: "Bash", Arg: "find . -iname 日本語 " + strings.Repeat("🚀 ", 20)},
		}},
		session.Message{Role: "user", Content: []session.Content{
			{Kind: session.ContentToolResult, Result: "🚀🚀 日本語の結果です " + strings.Repeat("長い出力です。", 20)},
		}},
	)

	for _, width := range []int{1, 5, 20, 40, 80, 120, 150, 200} {
		for height := 1; height <= 30; height++ {
			t.Run(fmt.Sprintf("width=%d/height=%d", width, height), func(t *testing.T) {
				m := openLoadedPreview(t, messages, width, height)

				view := m.View()
				if lines := len(strings.Split(view, "\n")); lines > height {
					t.Fatalf("height=%d: View() rendered %d lines:\n%s", height, lines, view)
				}
				for _, line := range strings.Split(view, "\n") {
					if got := lipgloss.Width(line); got > width {
						t.Fatalf("width=%d: line is %d cells wide: %q", width, got, line)
					}
				}
			})
		}
	}
}

// A message wrapped past the viewport must stay reachable by scrolling, not be
// cut behind an ellipsis: wrapping turns overflow into more lines, where
// truncating would just lose it. Checked expanded, since capped is where the
// held-back marker deliberately appears.
func TestPreviewLongMessageWrapsRatherThanBeingTruncated(t *testing.T) {
	long := strings.Repeat("supercalifragilisticexpialidocious wonderful phrase indeed ", 30)
	// 72 columns: narrow enough to force wrapping, wide enough that the footer's
	// own hint text (unrelated to what this test checks) does not itself need
	// truncating and salt the "no ellipsis anywhere" assertion below.
	m := openLoadedPreview(t, []session.Message{{Role: "user", Text: long}}, 72, 8)
	m.previewFull = true

	total := len(m.previewLines())
	capacity := m.previewCapacity()
	if total <= capacity {
		t.Fatalf("fixture does not exceed the viewport: %d lines, capacity %d", total, capacity)
	}

	var seen strings.Builder
	for offset := 0; offset <= total-capacity; offset++ {
		m.previewOffset = offset
		seen.WriteString(m.View())
	}
	if !strings.Contains(seen.String(), "wonderful phrase indeed") {
		t.Errorf("scrolling through the whole message lost content instead of wrapping it")
	}
	if strings.Contains(seen.String(), "…") {
		t.Errorf("View() truncated the message with an ellipsis instead of wrapping it")
	}
}

func TestPreviewHeaderShowsTheSessionNameCwdAndBranch(t *testing.T) {
	sessions := []session.Session{{
		Agent: "claude", ID: "s1", Name: "export report as PDF",
		Cwd: "/Users/dev/git/acme/billing", GitBranch: "master", LastActive: time.Now(),
	}}
	m := sized(t, newModel(Config{Sessions: sessions, Preview: func(session.Session) ([]session.Message, error) {
		return nil, nil
	}}), 80, 24)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(cmd())
	view := next.(model).View()

	for _, want := range []string{"export report as PDF", "billing", "master"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() header missing %q:\n%s", want, view)
		}
	}
}

func TestPreviewFooterNamesTheKeys(t *testing.T) {
	m := sized(t, newModel(Config{Sessions: testSessions(), Preview: func(session.Session) ([]session.Message, error) {
		return nil, nil
	}}), 80, 24)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(cmd())
	view := next.(model).View()

	for _, want := range []string{"↑/↓", "^u/^d", "scroll", "resume", "esc back", "^j", "jump"} {
		if !strings.Contains(view, want) {
			t.Errorf("preview footer missing %q:\n%s", want, view)
		}
	}
	if got := lipgloss.Width(previewFooterHint); got > 78 {
		t.Errorf("previewFooterHint is %d cells wide, want it to fit inside an 80-column terminal alongside the 2-cell gutter", got)
	}
}

// Pinned against the approved layout: "you"/"cc" opens the message that said
// something, and a collapsed tool call or its result never carries one, since
// neither is something the owner said or wrote.
func TestPreviewContentLinesShowRoleOnFirstLineOnlyAndNeverOnToolCallsOrResults(t *testing.T) {
	messages := []session.Message{
		{Role: "user", Text: "oh revert that, I don't want that please"},
		{Role: "assistant", Content: []session.Content{{Kind: session.ContentToolCall, Tool: "Edit", Arg: "settings.json"}}},
		{Role: "user", Content: []session.Content{{Kind: session.ContentToolResult, Result: "ok"}}},
		{Role: "assistant", Text: "Reverted. settings.json back to original."},
	}
	lines := previewContentLines(messages, 80, previewMessageCap)
	if len(lines) != 4 {
		t.Fatalf("previewContentLines() = %+v, want 4 lines for 4 short one-line messages", lines)
	}
	if !strings.Contains(lines[0], "you") || !strings.Contains(lines[0], "oh revert that") {
		t.Errorf("lines[0] = %q, want the user label and text", lines[0])
	}
	if strings.Contains(lines[1], "cc") {
		t.Errorf("lines[1] = %q, a tool call line must not carry a role label", lines[1])
	}
	if !strings.Contains(lines[1], "Edit") || !strings.Contains(lines[1], "settings.json") {
		t.Errorf("lines[1] = %q, want the tool name and its argument", lines[1])
	}
	if strings.Contains(lines[2], "you") {
		t.Errorf("lines[2] = %q, a tool result line must not carry a role label", lines[2])
	}
	if !strings.Contains(lines[2], "ok") {
		t.Errorf("lines[2] = %q, want the tool result text", lines[2])
	}
	if !strings.Contains(lines[3], "cc") || !strings.Contains(lines[3], "Reverted") {
		t.Errorf("lines[3] = %q, want the assistant label and text", lines[3])
	}
}

// A tool call's argument or a tool result's line can run far longer than any
// terminal (a deep path, a long log line), and unlike prose neither wraps —
// they are meant to stay one line — so the only thing standing between them
// and a line wider than the terminal is clampLine. Swept across every width
// from 1 to 200 rather than a handful of round numbers, per the brief.
func TestPreviewToolLinesNeverExceedTerminalWidth(t *testing.T) {
	messages := []session.Message{
		{Role: "assistant", Content: []session.Content{
			{Kind: session.ContentToolCall, Tool: "Bash", Arg: "find . -iname '*日本語*' 🚀🚀🚀 " + strings.Repeat("very-long-argument-segment-", 40)},
		}},
		{Role: "user", Content: []session.Content{
			{Kind: session.ContentToolResult, Result: "🚀 結果です emoji and 日本語 mixed in, then " + strings.Repeat("x", 500)},
		}},
	}

	for width := 1; width <= 200; width++ {
		for _, line := range previewContentLines(messages, width, previewMessageCap) {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width=%d: tool line is %d cells wide: %q", width, got, line)
			}
		}
	}
}

func TestPreviewCtrlJOnLiveSessionCallsJumpWithItsPID(t *testing.T) {
	var got int
	called := false
	m := newModel(Config{Sessions: testSessions(), Preview: func(session.Session) ([]session.Message, error) {
		return nil, nil
	}, Jump: func(_ context.Context, pid int) error {
		called, got = true, pid
		return nil
	}})
	m.cursor = 0 // testSessions()[0] is the live row, PID 7.

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(cmd())
	previewing := next.(model)

	next, cmd = previewing.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	result := next.(model)

	if cmd == nil {
		t.Fatal("Update() returned no Cmd, want the jump handed to one")
	}
	cmd()
	if !called {
		t.Fatal("Jump was not called for the live previewed session")
	}
	if got != 7 {
		t.Errorf("Jump called with pid %d, want 7", got)
	}
	if !result.previewing {
		t.Error("model.previewing = false, want ctrl+j to leave the preview open")
	}
}

// A history row has no process behind it, and the preview must say so rather
// than silently doing nothing — the same contract browsing mode's own ctrl+j
// already keeps for a history row.
func TestPreviewCtrlJOnHistorySessionDoesNotCallJumpAndReportsWhy(t *testing.T) {
	called := false
	m := newModel(Config{Sessions: testSessions(), Preview: func(session.Session) ([]session.Message, error) {
		return nil, nil
	}, Jump: func(context.Context, int) error {
		called = true
		return nil
	}})
	m.cursor = 1 // testSessions()[1] is the history row, no process behind it.

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(cmd())
	previewing := next.(model)

	next, cmd = previewing.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	result := next.(model)

	if cmd != nil {
		cmd()
	}
	if called {
		t.Error("Jump was called for a history session, which has no process to jump to")
	}
	if result.status == "" {
		t.Error("model.status is empty, want the footer to say why nothing happened")
	}
	if !result.previewing {
		t.Error("model.previewing = false, want ctrl+j on a history session to leave the preview open")
	}
}

// A jump failure must land in the preview's own footer rather than knocking
// the owner back to the list: the preview is still the screen they were
// looking at, and the failure did not change what it should show.
func TestPreviewCtrlJErrorLandsInFooterAndPreviewStaysOpen(t *testing.T) {
	m := newModel(Config{Sessions: testSessions(), Preview: func(session.Session) ([]session.Message, error) {
		return nil, nil
	}, Jump: func(context.Context, int) error {
		return errors.New("no iTerm2 pane found for pid 7")
	}})
	m.cursor = 0

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(cmd())
	previewing := next.(model)

	next, cmd = previewing.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	next, followUp := next.(model).Update(cmd())
	result := next.(model)

	if !strings.Contains(result.View(), "no iTerm2 pane found for pid 7") {
		t.Errorf("View() does not surface the jump error:\n%s", result.View())
	}
	if !result.previewing {
		t.Error("model.previewing = false, want a failed jump to leave the preview open")
	}
	if result.done {
		t.Error("model.done = true, want a failed jump to leave the picker running")
	}
	if followUp != nil {
		t.Error("handling the jump result returned a non-nil Cmd, want nil so a failed jump cannot quit the program")
	}
}

// The progress line is a sentence, so it gets its own line in the header rather
// than being crushed into the status column's two dozen cells. That line has to
// be counted, or the frame overflows and bubbletea drops the top of it.
func TestPreviewHeaderCarriesTheJobDetailAndStillFits(t *testing.T) {
	job := session.Session{
		Agent: "claude", ID: "job", Name: "search reindex rollout",
		Cwd: "/Users/dev/git/acme/ledger", GitBranch: "feature/eng-3150",
		JobState: "working", Detail: "Verifying all 5 fixed cases across 4 runs", Tokens: 43223,
		LastActive: time.Now(),
	}

	for _, height := range []int{6, 10, 20, 30} {
		t.Run(fmt.Sprintf("height=%d", height), func(t *testing.T) {
			m := sized(t, newModel(Config{Sessions: []session.Session{job}, Preview: func(session.Session) ([]session.Message, error) {
				return previewTestMessages(40), nil
			}}), 110, height)
			m.cursor = 0

			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			next, _ = next.(model).Update(cmd())
			view := next.(model).View()

			lines := strings.Split(view, "\n")
			if len(lines) > height {
				t.Fatalf("View() rendered %d lines in a %d-line terminal:\n%s", len(lines), height, view)
			}
			if height < 10 {
				return
			}
			for _, want := range []string{"working", "Verifying all 5 fixed cases", "43,223 tokens"} {
				if !strings.Contains(view, want) {
					t.Errorf("the preview header omits %q:\n%s", want, view)
				}
			}
		})
	}
}

// An ordinary session has no job record, and its header must not grow a blank
// line waiting for one.
func TestPreviewHeaderStaysThreeLinesWithoutAJobRecord(t *testing.T) {
	m := openLoadedPreview(t, previewTestMessages(10), 110, 24)
	if got := m.previewHeaderHeight(); got != previewHeaderLines {
		t.Errorf("previewHeaderHeight() = %d, want %d for a session with no job record", got, previewHeaderLines)
	}
	if len(m.previewHeaderBox()) != previewHeaderLines {
		t.Errorf("previewHeaderBox() returned %d lines, want %d", len(m.previewHeaderBox()), previewHeaderLines)
	}
}

// The preview's footer is pinned the same way the list's is: a session with two
// messages must not float its hint line into the middle of the screen.
func TestPreviewViewKeepsTheFooterOnTheLastLine(t *testing.T) {
	for _, count := range []int{0, 1, 2, 5, 40} {
		t.Run(fmt.Sprintf("messages=%d", count), func(t *testing.T) {
			m := openLoadedPreview(t, previewTestMessages(count), 110, 20)
			lines := strings.Split(m.View(), "\n")

			if len(lines) != 20 {
				t.Errorf("View() rendered %d lines, want the frame to fill the 20-line terminal", len(lines))
			}
			if last := lines[len(lines)-1]; !strings.Contains(last, "scroll") && !strings.Contains(last, "resume") {
				t.Errorf("the last line is %q, want the preview footer", last)
			}
		})
	}
}

func longMessage(lines int) session.Message {
	body := make([]string, lines)
	for i := range body {
		body[i] = fmt.Sprintf("body line %02d", i)
	}
	return session.Message{Role: "assistant", Text: strings.Join(body, "\n")}
}

// One screenful of curl examples used to fill the whole viewport, scrolling the
// messages either side of it — the reason for opening the preview — out of
// reach.
func TestPreviewCapsOneMessageAndKeepsItsNeighboursVisible(t *testing.T) {
	messages := []session.Message{
		{Role: "user", Text: "before"},
		longMessage(40),
		{Role: "user", Text: "after"},
	}

	lines := previewContentLines(messages, 100, previewMessageCap)
	// One line each for the neighbours; the long one gets previewMessageCap
	// content lines plus its held-back marker.
	if want := 2 + previewMessageCap + 1; len(lines) != want {
		t.Fatalf("previewContentLines() returned %d lines, want %d", len(lines), want)
	}

	joined := strings.Join(lines, "\n")
	for _, want := range []string{"before", "after", "body line 00"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the capped preview lost %q:\n%s", want, joined)
		}
	}
	// 40 body lines, 12 kept, so 28 held back.
	if !strings.Contains(joined, "+28 more lines") {
		t.Errorf("the marker does not say how many lines were held back:\n%s", joined)
	}
	if strings.Contains(joined, "body line 39") {
		t.Errorf("the capped preview drew past the cap:\n%s", joined)
	}
}

// The head is kept, not the tail: a message is read from the top, and its
// opening line is what identifies it.
func TestPreviewCapKeepsTheHeadOfTheMessage(t *testing.T) {
	lines := previewContentLines([]session.Message{longMessage(30)}, 100, previewMessageCap)

	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "body line 00") {
		t.Errorf("the cap dropped the opening line:\n%s", joined)
	}
	if !strings.Contains(joined, fmt.Sprintf("body line %02d", previewMessageCap-1)) {
		t.Errorf("the cap kept fewer lines than it should:\n%s", joined)
	}
}

func TestPreviewCapLeavesShortMessagesAlone(t *testing.T) {
	for _, count := range []int{1, previewMessageCap - 1, previewMessageCap} {
		t.Run(fmt.Sprintf("lines=%d", count), func(t *testing.T) {
			lines := previewContentLines([]session.Message{longMessage(count)}, 100, previewMessageCap)
			if len(lines) != count {
				t.Errorf("previewContentLines() returned %d lines, want %d untouched", len(lines), count)
			}
			if strings.Contains(strings.Join(lines, "\n"), "more line") {
				t.Error("a message within the cap was given a held-back marker")
			}
		})
	}
}

// One held-back line is reachable only because the marker is an extra line
// rather than one of the limit. Spending a content line on it would make the
// singular case impossible and leave the wording untestable.
func TestPreviewCapMarkerIsSingularForOneHeldLine(t *testing.T) {
	lines := previewContentLines([]session.Message{longMessage(previewMessageCap + 1)}, 100, previewMessageCap)

	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "+1 more line") || strings.Contains(joined, "+1 more lines") {
		t.Errorf("marker in %q, want a singular +1 more line", joined)
	}
}

// A cap the owner cannot lift makes the held-back text unreachable, which is
// exactly what wrapping rather than truncating was meant to avoid.
func TestPreviewExpandTogglesTheCap(t *testing.T) {
	m := openLoadedPreview(t, []session.Message{longMessage(40)}, 100, 20)

	capped := len(m.previewLines())
	if capped != previewMessageCap+1 {
		t.Fatalf("previewLines() = %d, want %d content lines plus the marker", capped, previewMessageCap)
	}

	next, _ := m.Update(keyMsg('e'))
	full := next.(model)
	if !full.previewFull {
		t.Fatal("e did not expand the preview")
	}
	if len(full.previewLines()) != 40 {
		t.Errorf("previewLines() = %d expanded, want all 40", len(full.previewLines()))
	}

	next, _ = full.Update(keyMsg('e'))
	if len(next.(model).previewLines()) != previewMessageCap+1 {
		t.Error("e did not collapse the preview again")
	}
}

// Collapsing removes lines under the viewport, so an offset left where it was
// would point past the end.
func TestPreviewExpandKeepsTheOffsetInBounds(t *testing.T) {
	m := openLoadedPreview(t, []session.Message{longMessage(200)}, 100, 12)

	expanded, _ := m.Update(keyMsg('e'))
	scrolled := press(t, expanded.(model), tea.KeyMsg{Type: tea.KeyCtrlD}, 30)
	if scrolled.previewOffset == 0 {
		t.Fatal("setup: never scrolled away from the top")
	}

	collapsed, _ := scrolled.Update(keyMsg('e'))
	got := collapsed.(model)
	limit := max(len(got.previewLines())-got.previewCapacity(), 0)
	if got.previewOffset > limit {
		t.Errorf("previewOffset = %d, want at most %d after collapsing", got.previewOffset, limit)
	}
	if lines := strings.Split(got.View(), "\n"); len(lines) != 12 {
		t.Errorf("View() rendered %d lines in a 12-line terminal:\n%s", len(lines), got.View())
	}
}

func TestPreviewCapMarkerFitsEveryWidth(t *testing.T) {
	for width := 1; width <= 120; width++ {
		for _, line := range previewContentLines([]session.Message{longMessage(40)}, width, previewMessageCap) {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width=%d: line is %d cells: %q", width, got, line)
			}
		}
	}
}
