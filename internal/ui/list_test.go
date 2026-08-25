package ui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/eduardvoiculescu/agent-sessions/internal/action"
	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

func testSessions() []session.Session {
	now := time.Now()
	return []session.Session{
		{Agent: "claude", ID: "live", Name: "webapp-8e", Cwd: "/Users/dev/git/webapp", Live: true, PID: 7, Status: "running", LastActive: now},
		{Agent: "claude", ID: "hist", Name: "Fix search ranking", Cwd: "/Users/dev/git/billing", GitBranch: "eng-3140", LastActive: now.Add(-time.Hour)},
	}
}

func keyMsg(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// Enter used to resume a history row immediately and ask "is running
// elsewhere. Resume anyway?" first for a live one. The preview subsumes both:
// Enter always opens it, live row or not, and never resumes or asks a
// yes/no question directly.
func TestModelEnterOpensThePreviewRatherThanResumingOrConfirming(t *testing.T) {
	tests := []struct {
		name   string
		cursor int
		wantID string
	}{
		{name: "live row", cursor: 0, wantID: "live"},
		{name: "history row", cursor: 1, wantID: "hist"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(Config{Sessions: testSessions(), Preview: func(session.Session) ([]session.Message, error) {
				return nil, nil
			}})
			m.cursor = tt.cursor

			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			got := next.(model)

			if got.done || got.chosen {
				t.Fatal("model.done or .chosen = true, want Enter to only open the preview")
			}
			if got.confirming {
				t.Error("model.confirming = true, want the preview to replace the confirmation prompt")
			}
			if !got.previewing {
				t.Fatal("model.previewing = false, want true after Enter")
			}
			if got.previewTarget.ID != tt.wantID {
				t.Errorf("previewTarget.ID = %q, want %q", got.previewTarget.ID, tt.wantID)
			}
			if cmd == nil {
				t.Error("Update() returned no Cmd, want the transcript read handed to one")
			}
		})
	}
}

// esc leaves the preview without resuming, back at the list it was opened
// from.
func TestModelPreviewEscReturnsToTheListWithoutResuming(t *testing.T) {
	m := newModel(Config{Sessions: testSessions()})
	m.cursor = 0

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	back := next.(model)

	if back.done || back.chosen {
		t.Error("model.done or .chosen = true, want esc to leave without resuming")
	}
	if back.previewing {
		t.Error("model.previewing = true, want esc to return to the list")
	}
}

func TestModelForkKeySetsFork(t *testing.T) {
	m := newModel(Config{Sessions: testSessions()})
	m.cursor = 0

	next, _ := m.Update(keyMsg('f'))
	got := next.(model)

	if !got.done {
		t.Fatal("model.done = false, want true after f")
	}
	if !got.choice.Fork {
		t.Error("choice.Fork = false, want true for f")
	}
	if got.confirming {
		t.Error("model.confirming = true, want fork to skip confirmation")
	}
}

func TestModelFilterNarrowsRowsAndResetsCursor(t *testing.T) {
	m := newModel(Config{Sessions: testSessions()})
	m.cursor = 1

	next, _ := m.Update(keyMsg('/'))
	filtering := next.(model)
	for _, r := range "search ranking" {
		next, _ = filtering.Update(keyMsg(r))
		filtering = next.(model)
	}

	if len(filtering.visible()) != 1 {
		t.Fatalf("visible() returned %d rows, want 1", len(filtering.visible()))
	}
	if filtering.visible()[0].ID != "hist" {
		t.Errorf("visible()[0].ID = %q, want %q", filtering.visible()[0].ID, "hist")
	}
	if filtering.cursor != 0 {
		t.Errorf("cursor = %d, want 0 after filtering", filtering.cursor)
	}
}

func TestModelTickRefreshesLiveState(t *testing.T) {
	refreshed := []session.Session{
		{Agent: "claude", ID: "live", Name: "webapp-8e", Live: true, PID: 7, Status: "waiting: input needed", LastActive: time.Now()},
	}
	m := newModel(Config{Sessions: testSessions(), Refresh: func() []session.Session { return refreshed }})

	next, _ := m.Update(tickMsg{})
	got := next.(model)

	if got.visible()[0].Status != "waiting: input needed" {
		t.Errorf("status = %q, want the refreshed status", got.visible()[0].Status)
	}
	if len(got.visible()) != 2 {
		t.Errorf("visible() returned %d rows, want the history row kept", len(got.visible()))
	}
}

func TestModelQuitAborts(t *testing.T) {
	m := newModel(Config{Sessions: testSessions()})

	next, _ := m.Update(keyMsg('q'))
	got := next.(model)

	if !got.done {
		t.Error("model.done = false, want true after q")
	}
	if got.choice.Session.ID != "" {
		t.Errorf("choice.Session.ID = %q, want it empty after quitting", got.choice.Session.ID)
	}
}

func TestModelViewShowsLiveDividerAndStatuses(t *testing.T) {
	view := newModel(Config{Sessions: testSessions()}).View()

	for _, want := range []string{"webapp-8e", "running", "Fix search ranking", "eng-3140"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q:\n%s", want, view)
		}
	}

	table := tableLines(view)
	dividerIdx := strings.Index(table, "─")
	if dividerIdx == -1 {
		t.Fatalf("View() missing the live/history divider:\n%s", view)
	}

	liveIdx := strings.Index(table, "webapp-8e")
	histIdx := strings.Index(table, "Fix search ranking")
	if liveIdx == -1 || histIdx == -1 {
		t.Fatalf("could not locate both rows in view:\n%s", view)
	}
	if !(liveIdx < dividerIdx && dividerIdx < histIdx) {
		t.Errorf("want live row, then divider, then history row; got live=%d divider=%d hist=%d\n%s", liveIdx, dividerIdx, histIdx, view)
	}
}

func TestModelViewFooterAdvertisesTheCommandPalette(t *testing.T) {
	view := newModel(Config{Sessions: testSessions()}).View()

	if !strings.Contains(view, "^p") {
		t.Errorf("View() footer does not mention ^p, the command palette key:\n%s", view)
	}
}

// The footer's key hints are ordered by importance because truncation eats the
// tail first: a narrow terminal must still show what matters, not just what
// sorts alphabetically first.
func TestModelViewFooterFitsAnEightyColumnTerminal(t *testing.T) {
	m := sized(t, newModel(Config{Sessions: testSessions()}), 80, 24)
	view := m.View()

	for _, want := range []string{"preview", "actions", "jump", "live", "filter", "help", "quit"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() footer at 80 columns lost %q to truncation:\n%s", want, view)
		}
	}
	// Checked on the footer line alone: the rows above it truncate by design,
	// so an ellipsis anywhere in the frame proves nothing about the footer.
	lines := strings.Split(view, "\n")
	if footer := lines[len(lines)-1]; strings.Contains(footer, "…") {
		t.Errorf("View() footer at 80 columns was truncated: %q", footer)
	}
}

func TestModelCtrlJOnLiveRowCallsJumpWithItsPID(t *testing.T) {
	var got int
	called := false
	m := newModel(Config{Sessions: testSessions(), Jump: func(_ context.Context, pid int) error {
		called, got = true, pid
		return nil
	}})
	m.cursor = 0 // testSessions()[0] is the live row, PID 7.

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	result := next.(model)

	if cmd == nil {
		t.Fatal("Update() returned no Cmd, want the jump handed to one")
	}
	cmd()
	if !called {
		t.Fatal("Jump was not called for the live row")
	}
	if got != 7 {
		t.Errorf("Jump called with pid %d, want 7", got)
	}
	if result.done {
		t.Error("model.done = true, want ctrl+j to leave the picker running")
	}
}

// The jump shells out to ps and osascript, and osascript sits on macOS's
// Automation consent dialog until a human answers it. Update has to hand that
// off to a tea.Cmd and return, or rendering, the tick and ctrl+c freeze for as
// long as the jump takes.
func TestModelCtrlJDoesNotRunTheJumpInsideUpdate(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	m := newModel(Config{Sessions: testSessions(), Jump: func(context.Context, int) error {
		close(started)
		<-release
		return nil
	}})
	m.cursor = 0

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if cmd == nil {
		t.Fatal("Update() returned no Cmd, want the jump handed to one")
	}
	select {
	case <-started:
		t.Fatal("Jump ran inside Update, which blocks the event loop until it returns")
	default:
	}

	if _, quit := next.(model).Update(tea.KeyMsg{Type: tea.KeyCtrlC}); quit == nil {
		t.Error("ctrl+c was ignored while a jump was outstanding")
	}

	results := make(chan tea.Msg, 1)
	go func() { results <- cmd() }()
	<-started
	close(release)
	if msg, ok := (<-results).(jumpResultMsg); !ok {
		t.Errorf("cmd() returned %T, want a jumpResultMsg", msg)
	}
}

func TestModelCtrlJOnHistoryRowDoesNotCallJumpAndReportsWhy(t *testing.T) {
	called := false
	m := newModel(Config{Sessions: testSessions(), Jump: func(context.Context, int) error {
		called = true
		return nil
	}})
	m.cursor = 1 // testSessions()[1] is the history row, no process behind it.

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	result := next.(model)

	if cmd != nil {
		cmd()
	}
	if called {
		t.Error("Jump was called for a history row, which has no process to jump to")
	}
	if result.status == "" {
		t.Error("model.status is empty, want the footer to say why nothing happened")
	}
	if result.done {
		t.Error("model.done = true, want ctrl+j on a history row to leave the picker running")
	}
}

// A jump failure is reported the same way an action failure is: in the
// footer, with the picker still running. m.done is asserted directly, not
// just the rendered view, since a regression that quit the picker while still
// happening to render the error text would pass a view-only check.
func TestModelCtrlJErrorLandsInFooterAndPickerSurvives(t *testing.T) {
	m := newModel(Config{Sessions: testSessions(), Jump: func(context.Context, int) error {
		return errors.New("no iTerm2 pane found for pid 7")
	}})
	m.cursor = 0

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	next, followUp := next.(model).Update(cmd())
	result := next.(model)

	if !strings.Contains(result.View(), "no iTerm2 pane found for pid 7") {
		t.Errorf("View() does not surface the jump error:\n%s", result.View())
	}
	if result.done {
		t.Error("model.done = true, want a failed jump to leave the picker running")
	}
	if followUp != nil {
		t.Error("handling the jump result returned a non-nil Cmd, want nil so a failed jump cannot quit the program")
	}
}

// The failure messages outlive their cause: after a failed jump the owner moves
// to a live row and jumps successfully, and the footer must stop explaining the
// jump that failed two keypresses ago.
func TestModelCtrlJSuccessClearsAPreviousFailure(t *testing.T) {
	fail := true
	m := newModel(Config{Sessions: testSessions(), Jump: func(context.Context, int) error {
		if fail {
			return errors.New("no iTerm2 pane found for pid 7")
		}
		return nil
	}})
	m.cursor = 0

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	next, _ = next.(model).Update(cmd())
	if next.(model).status == "" {
		t.Fatal("model.status is empty after a failed jump, want the reason in the footer")
	}

	fail = false
	next, cmd = next.(model).Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	next, _ = next.(model).Update(cmd())

	if got := next.(model).status; got != "" {
		t.Errorf("model.status = %q after a successful jump, want it cleared", got)
	}
}

// A row can be live with no pid behind it: the live registry publishes the
// status of a session it read from a state file, and Merge keeps the row even
// when no process was matched to it. killAction guards on PID > 0 for the same
// reason.
func TestModelCtrlJOnALiveRowWithNoPIDDoesNotJump(t *testing.T) {
	sessions := testSessions()
	sessions[0].PID = 0
	called := false
	m := newModel(Config{Sessions: sessions, Jump: func(context.Context, int) error {
		called = true
		return nil
	}})
	m.cursor = 0

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if cmd != nil {
		cmd()
	}
	if called {
		t.Error("Jump was called for a live row with no pid")
	}
	if next.(model).status == "" {
		t.Error("model.status is empty, want the footer to say why nothing happened")
	}
}

// A live row is jumpable in principle, but the picker cannot pretend jump
// works when nothing wired it up — the key must still say why rather than
// silently do nothing. The message deliberately does not name iTerm2 or macOS:
// pickerConfig always wires the jump, so this is a programming slip, not the
// platform story, which Jump's own runtime.GOOS check reports.
func TestModelCtrlJWithNoJumpFuncReportsUnavailable(t *testing.T) {
	m := newModel(Config{Sessions: testSessions()})
	m.cursor = 0

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	result := next.(model)

	if cmd != nil {
		t.Error("Update() returned a Cmd with no jump wired, want nil")
	}
	if !strings.Contains(result.View(), "jump is unavailable") {
		t.Errorf("View() = %q, want it to explain jump is unavailable", result.View())
	}
	if result.done {
		t.Error("model.done = true, want an unwired jump to leave the picker running")
	}
}

// The preview replaced the "is running elsewhere" confirmation, and it must
// keep the same guarantee that confirmation had: a tick lands between the
// Enter that opens the preview and the Enter that resumes it, re-sorting the
// rows underneath the cursor, and the second Enter still has to resume the
// session the preview was opened for, not whatever slid into the cursor's
// slot. This is the scenario a prior confirmation regression got wrong by
// resolving its target positionally after a message boundary.
func TestModelTickBetweenPreviewOpenAndResumeResumesThePreviewedSession(t *testing.T) {
	base := time.Now()
	sessions := []session.Session{
		{Agent: "claude", ID: "alpha", Name: "Alpha", Live: true, LastActive: base},
		{Agent: "claude", ID: "beta", Name: "Beta", LastActive: base.Add(-time.Hour)},
	}
	refreshed := []session.Session{
		{Agent: "claude", ID: "beta", Name: "Beta", Live: true, PID: 55, Status: "running", LastActive: base.Add(time.Hour)},
	}

	m := newModel(Config{
		Sessions: sessions,
		Refresh:  func() []session.Session { return refreshed },
		Preview:  func(session.Session) ([]session.Message, error) { return nil, nil },
	})
	m.cursor = 0

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	previewing := next.(model)
	if !previewing.previewing {
		t.Fatal("model.previewing = false, want true after Enter")
	}
	if cmd != nil {
		next, _ = previewing.Update(cmd())
		previewing = next.(model)
	}

	next, _ = previewing.Update(tickMsg{})
	ticked := next.(model)
	if ticked.visible()[0].ID != "beta" {
		t.Fatalf("visible()[0].ID = %q, want %q after the tick re-sorts the rows", ticked.visible()[0].ID, "beta")
	}

	next, _ = ticked.Update(tea.KeyMsg{Type: tea.KeyEnter})
	resumed := next.(model)

	if !resumed.chosen {
		t.Fatal("model.chosen = false, want true after the second Enter")
	}
	if resumed.choice.Session.ID != "alpha" {
		t.Errorf("choice.Session.ID = %q, want %q, the session the preview was opened for", resumed.choice.Session.ID, "alpha")
	}
}

func TestModelTickDropsLiveStateForExitedSession(t *testing.T) {
	m := newModel(Config{Sessions: testSessions(), Refresh: func() []session.Session { return nil }})

	next, _ := m.Update(tickMsg{})
	got := next.(model)

	for _, s := range got.visible() {
		if s.ID != "live" {
			continue
		}
		if s.Live {
			t.Error("Live = true, want false once the process no longer appears in refresh()")
		}
		if s.Status != "" {
			t.Errorf("Status = %q, want empty once the process has exited", s.Status)
		}
		if s.PID != 0 {
			t.Errorf("PID = %d, want 0 once the process has exited", s.PID)
		}
	}
}

func TestModelMoveClampsAtTopAndBottom(t *testing.T) {
	m := newModel(Config{Sessions: testSessions()})
	m.cursor = 0

	next, _ := m.Update(keyMsg('k'))
	got := next.(model)
	if got.cursor != 0 {
		t.Errorf("cursor = %d, want 0, up should clamp at the top", got.cursor)
	}

	next, _ = got.Update(keyMsg('j'))
	got = next.(model)
	if got.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 after moving down", got.cursor)
	}

	next, _ = got.Update(keyMsg('j'))
	got = next.(model)
	if got.cursor != 1 {
		t.Errorf("cursor = %d, want 1, down should clamp at the bottom", got.cursor)
	}

	next, _ = got.Update(keyMsg('k'))
	got = next.(model)
	if got.cursor != 0 {
		t.Errorf("cursor = %d, want 0 after moving up", got.cursor)
	}
}

func TestModelFilterBackspaceWidensRowsAndKeepsCursorInRange(t *testing.T) {
	m := newModel(Config{Sessions: testSessions()})

	next, _ := m.Update(keyMsg('/'))
	filtering := next.(model)
	for _, r := range "search ranking" {
		next, _ = filtering.Update(keyMsg(r))
		filtering = next.(model)
	}
	if len(filtering.visible()) != 1 {
		t.Fatalf("visible() returned %d rows after typing, want 1", len(filtering.visible()))
	}

	for range "search ranking" {
		next, _ = filtering.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		filtering = next.(model)
	}

	if filtering.query != "" {
		t.Errorf("query = %q, want empty after backspacing the whole filter", filtering.query)
	}
	if len(filtering.visible()) != 2 {
		t.Fatalf("visible() returned %d rows after clearing the filter, want 2", len(filtering.visible()))
	}
	if filtering.cursor != 0 {
		t.Errorf("cursor = %d, want 0 within range", filtering.cursor)
	}
}

func manySessions(n int) []session.Session {
	now := time.Now()
	sessions := make([]session.Session, n)
	for i := range sessions {
		sessions[i] = session.Session{
			Agent:      "claude",
			ID:         fmt.Sprintf("id-%02d", i),
			Name:       fmt.Sprintf("row-%02d", i),
			LastActive: now.Add(-time.Duration(i) * time.Minute),
		}
	}
	return sessions
}

func sized(t *testing.T, m model, width, height int) model {
	t.Helper()
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return next.(model)
}

func renderedRows(view string) []string {
	var rows []string
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "row-") {
			rows = append(rows, line)
		}
	}
	return rows
}

// tableLines strips the bordered header block, keeping only the column header
// and the row table below it. The block's border is drawn from the same "─"
// rune as the live/history divider, so a test looking for the divider has to
// search here instead of the whole view.
func tableLines(view string) string {
	var kept []string
	for _, line := range strings.Split(view, "\n") {
		bare := stripSGR(line)
		if strings.HasPrefix(bare, "╭") || strings.HasPrefix(bare, "│") || strings.HasPrefix(bare, "╰") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

var sgrPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

// stripSGR drops the colour sequences lipgloss emits whenever the terminal
// claims colour support — CLICOLOR_FORCE=1, say — which otherwise sit in front
// of the border rune a prefix test is looking for and make the suite pass or
// fail by environment.
func stripSGR(line string) string {
	return sgrPattern.ReplaceAllString(line, "")
}

func press(t *testing.T, m model, msg tea.KeyMsg, times int) model {
	t.Helper()
	for range times {
		next, _ := m.Update(msg)
		m = next.(model)
	}
	return m
}

func TestModelViewFitsTheTerminalHeight(t *testing.T) {
	m := sized(t, newModel(Config{Sessions: manySessions(60)}), 200, 20)

	view := m.View()
	if lines := len(strings.Split(view, "\n")); lines > 20 {
		t.Errorf("View() rendered %d lines, want at most the terminal height 20:\n%s", lines, view)
	}
	if got, want := len(renderedRows(view)), m.rowCapacity(m.visible()); got != want {
		t.Errorf("View() rendered %d rows, want %d (rowCapacity)", got, want)
	}
}

// The header block costs five lines and the column header a sixth; a
// rowCapacity that forgets either lets the frame grow past the terminal and
// bubbletea silently drops lines off the top, hiding the header and the
// cursor. This is the regression the picker shipped once before, so the
// assertion is the actual line count against the actual terminal height, at
// heights both comfortable and barely-there, never a hand-derived formula
// that could drift from rowCapacity the same way the bug did.
func TestModelViewNeverExceedsTerminalHeight(t *testing.T) {
	sessions := manySessions(200)
	for i := range 5 {
		sessions[i].Live = true
		sessions[i].Status = "running"
	}

	for _, height := range []int{10, 14, 20, 40} {
		for _, width := range []int{80, 120, 200} {
			t.Run(fmt.Sprintf("height=%d/width=%d", height, width), func(t *testing.T) {
				m := sized(t, newModel(Config{Sessions: sessions, LoadErr: errors.New("boom")}), width, height)
				m = press(t, m, keyMsg('G'), 1)

				view := m.View()
				if lines := len(strings.Split(view, "\n")); lines > height {
					t.Errorf("View() rendered %d lines, want at most %d:\n%s", lines, height, view)
				}
				for _, line := range strings.Split(view, "\n") {
					if got := lipgloss.Width(line); got > width {
						t.Errorf("line exceeds terminal width (%d > %d): %q", got, width, line)
					}
				}
				assertSelectedRowRendered(t, m)
			})
		}
	}
}

// A terminal too short for the header block and at least one row must drop
// the header, not the rows: the rows are the content the picker exists to
// show.
func TestModelViewDropsTheHeaderBlockBeforeDroppingRows(t *testing.T) {
	sessions := manySessions(200)
	sessions[0].Live = true
	sessions[0].Status = "running"

	m := sized(t, newModel(Config{Sessions: sessions, LoadErr: errors.New("boom")}), 200, 8)
	view := m.View()

	if strings.Contains(view, "╭") {
		t.Errorf("View() drew the header block at a height too short to afford it:\n%s", view)
	}
	if len(renderedRows(view)) == 0 {
		t.Errorf("View() dropped every row instead of the header at a short terminal:\n%s", view)
	}
	if lines := len(strings.Split(view, "\n")); lines > 8 {
		t.Errorf("View() rendered %d lines, want at most 8:\n%s", lines, view)
	}
}

// At an ordinary size the sticky wordmark sits above the header block: the
// tool name and version on its first line, the loaded session count on its
// second, both beside the "AS" block glyph.
func TestModelViewShowsTheWordmarkAndVersion(t *testing.T) {
	sessions := manySessions(3)
	m := sized(t, newModel(Config{Sessions: sessions}), 120, 30)

	view := m.View()
	if !strings.Contains(view, wordmarkGlyphTop) || !strings.Contains(view, wordmarkGlyphBottom) {
		t.Errorf("View() does not draw the AS glyph:\n%s", view)
	}
	if !strings.Contains(view, "agent-sessions v"+Version) {
		t.Errorf("View() does not show the tool name and version beside the glyph:\n%s", view)
	}
	if !strings.Contains(view, fmt.Sprintf("%d sessions", len(sessions))) {
		t.Errorf("View() does not show the session count beside the glyph:\n%s", view)
	}
}

// The wordmark is pure decoration, while the header block still tells the
// owner how many sessions there are and the rows are the content the picker
// exists to show — so a short terminal gives up the wordmark first, the
// header block second, and never the rows. Heights are chosen from frameFor's
// own boundaries (13 keeps every chrome line, 9 has already dropped the
// wordmark, 7 has dropped the header too), not guessed, so a rowCapacity that
// stopped counting the wordmark's four lines would push every one of these
// frames past its terminal height instead of just reordering what is drawn.
func TestModelViewDropsTheWordmarkBeforeTheHeaderBlockBeforeRows(t *testing.T) {
	sessions := manySessions(30)

	full := sized(t, newModel(Config{Sessions: sessions}), 120, 13)
	fullView := full.View()
	if !strings.Contains(fullView, wordmarkGlyphTop) {
		t.Fatalf("setup: expected the wordmark at height 13:\n%s", fullView)
	}
	if !strings.Contains(fullView, "╭") {
		t.Fatalf("setup: expected the header block at height 13:\n%s", fullView)
	}
	assertFits(t, full, fullView, 13)

	headerOnly := sized(t, newModel(Config{Sessions: sessions}), 120, 9)
	headerView := headerOnly.View()
	if strings.Contains(headerView, wordmarkGlyphTop) {
		t.Errorf("View() drew the wordmark at a height too short to afford it alongside the header:\n%s", headerView)
	}
	if !strings.Contains(headerView, "╭") {
		t.Errorf("View() dropped the header block before the wordmark:\n%s", headerView)
	}
	if len(renderedRows(headerView)) == 0 {
		t.Errorf("View() dropped every row before the wordmark and the header:\n%s", headerView)
	}
	assertFits(t, headerOnly, headerView, 9)

	rowsOnly := sized(t, newModel(Config{Sessions: sessions}), 120, 7)
	rowsView := rowsOnly.View()
	if strings.Contains(rowsView, wordmarkGlyphTop) {
		t.Errorf("View() drew the wordmark at a height too short for it and the header:\n%s", rowsView)
	}
	if strings.Contains(rowsView, "╭") {
		t.Errorf("View() drew the header block at a height too short to afford it:\n%s", rowsView)
	}
	if len(renderedRows(rowsView)) == 0 {
		t.Errorf("View() dropped every row instead of both the wordmark and the header:\n%s", rowsView)
	}
	assertFits(t, rowsOnly, rowsView, 7)
}

// A block glyph cut mid-character reads as broken art, not a smaller
// wordmark, so wordmarkLines drops the whole glyph rather than truncate
// through it. The glyph is indented by the gutter, so it needs
// wordmarkMinWidth (9) cells, and 10 is the narrowest width where truncate's
// own reserved ellipsis cell lands exactly on the boundary between the glyph
// and the gap after it; this pins both sides of that boundary directly, since
// the width sweep elsewhere in this file does not happen to hit 6 through 11.
func TestWordmarkLinesDropsTheGlyphRatherThanCutThroughIt(t *testing.T) {
	for width := 1; width <= 15; width++ {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			m := sized(t, newModel(Config{Sessions: manySessions(3)}), width, 30)
			lines := m.wordmarkLines()
			if len(lines) != wordmarkBlockLines {
				t.Fatalf("wordmarkLines() returned %d lines, want %d", len(lines), wordmarkBlockLines)
			}

			for _, line := range lines {
				if got := lipgloss.Width(line); got > width {
					t.Errorf("line exceeds the %d-column terminal (%d cells): %q", width, got, line)
				}
			}

			hasFullGlyph := strings.Contains(lines[1], wordmarkGlyphTop) && strings.Contains(lines[2], wordmarkGlyphBottom)
			switch {
			case width <= wordmarkMinWidth && hasFullGlyph:
				t.Errorf("width=%d: glyph survived at a width too narrow for it intact: %q / %q", width, lines[1], lines[2])
			case width > wordmarkMinWidth && !hasFullGlyph:
				t.Errorf("width=%d: glyph was cut instead of shown whole: %q / %q", width, lines[1], lines[2])
			}
		})
	}
}

// assertFits checks the frame invariant the wordmark must not break: the
// rendered line count never exceeds the terminal height, and it matches
// rowCapacity's own accounting rather than a number hand-derived alongside it.
func assertFits(t *testing.T, m model, view string, height int) {
	t.Helper()
	if lines := len(strings.Split(view, "\n")); lines > height {
		t.Errorf("View() rendered %d lines, want at most %d:\n%s", lines, height, view)
	}
	if got, want := len(renderedRows(view)), m.rowCapacity(m.visible()); got != want {
		t.Errorf("View() rendered %d rows, want %d (rowCapacity)", got, want)
	}
}

func TestModelViewKeepsTheSelectedRowInsideTheWindow(t *testing.T) {
	m := sized(t, newModel(Config{Sessions: manySessions(60)}), 200, 20)

	m = press(t, m, keyMsg('j'), 59)
	if m.cursor != 59 {
		t.Fatalf("cursor = %d, want 59 at the bottom", m.cursor)
	}
	assertSelectedRowRendered(t, m)

	m = press(t, m, keyMsg('k'), 59)
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 back at the top", m.cursor)
	}
	assertSelectedRowRendered(t, m)
}

func assertSelectedRowRendered(t *testing.T, m model) {
	t.Helper()
	want := m.visible()[m.cursor].Name
	for _, line := range renderedRows(m.View()) {
		if strings.Contains(line, want) && strings.Contains(line, "▸") {
			return
		}
	}
	t.Errorf("selected row %q is outside the rendered window (cursor = %d):\n%s", want, m.cursor, m.View())
}

func TestModelViewDrawsTheDividerOnlyWhileTheBoundaryIsInTheWindow(t *testing.T) {
	rows := manySessions(40)
	for i := range 3 {
		rows[i].Live = true
		rows[i].Status = "running"
	}

	m := sized(t, newModel(Config{Sessions: rows}), 200, 20)
	if !strings.Contains(tableLines(m.View()), "─") {
		t.Errorf("View() has no divider while the boundary is in the window:\n%s", m.View())
	}

	scrolled := press(t, m, keyMsg('j'), 39)
	if strings.Contains(tableLines(scrolled.View()), "─") {
		t.Errorf("View() drew a divider with the boundary scrolled out of the window:\n%s", scrolled.View())
	}
}

func TestModelViewWithoutLiveRowsDrawsNoDivider(t *testing.T) {
	view := newModel(Config{Sessions: manySessions(5)}).View()

	if strings.Contains(tableLines(view), "─") {
		t.Errorf("View() drew a divider with no live rows to separate:\n%s", view)
	}
	if !strings.Contains(view, "history 5") {
		t.Errorf("View() counts line does not report the history count with no live rows:\n%s", view)
	}
}

func TestModelViewPlacesADemotedRowBelowTheDivider(t *testing.T) {
	base := time.Now()
	sessions := []session.Session{
		{Agent: "claude", ID: "stays", Name: "Stays", Live: true, PID: 1, Status: "running", LastActive: base},
		{Agent: "claude", ID: "exits", Name: "Exits", Live: true, PID: 2, Status: "running", LastActive: base.Add(-time.Minute)},
		{Agent: "claude", ID: "older", Name: "Older", LastActive: base.Add(-time.Hour)},
	}
	stillLive := []session.Session{sessions[0]}

	m := newModel(Config{Sessions: sessions, Refresh: func() []session.Session { return stillLive }})
	next, _ := m.Update(tickMsg{})
	view := next.(model).View()

	table := tableLines(view)
	dividerIdx := strings.Index(table, "─")
	demotedIdx := strings.Index(table, "Exits")
	if dividerIdx == -1 || demotedIdx == -1 {
		t.Fatalf("want both a divider and the demoted row in:\n%s", view)
	}
	if demotedIdx < dividerIdx {
		t.Errorf("the demoted row renders above the divider:\n%s", view)
	}
}

func TestModelViewReportsTheLoadError(t *testing.T) {
	joined := errors.Join(errors.New("claude: reading registry"), errors.New("codex: no such directory"))
	view := newModel(Config{Sessions: testSessions(), LoadErr: joined}).View()

	if !strings.Contains(view, "claude: reading registry codex: no such directory") {
		t.Errorf("View() does not report the load error on one line:\n%s", view)
	}
}

// The header block's counts are computed from the sessions the model actually
// holds, so they cannot drift from what the table below can show: one entry
// per distinct live status, in the order first seen, plus a history count.
func TestCountsLineReportsOneEntryPerDistinctLiveStatus(t *testing.T) {
	now := time.Now()
	sessions := []session.Session{
		{Agent: "claude", ID: "a", Live: true, Status: "busy", LastActive: now},
		{Agent: "claude", ID: "b", Live: true, Status: "busy", LastActive: now},
		{Agent: "claude", ID: "c", Live: true, Status: "idle", LastActive: now},
		{Agent: "claude", ID: "d", LastActive: now},
		{Agent: "claude", ID: "e", LastActive: now},
	}
	m := newModel(Config{Sessions: sessions})

	got := m.countsLine()
	for _, want := range []string{"sessions 5", "● busy 2", "● idle 1", "○ history 2"} {
		if !strings.Contains(got, want) {
			t.Errorf("countsLine() = %q, missing %q", got, want)
		}
	}
}

// The picker's filters line has to report what is really active, not a
// guess: since Filter is an opaque closure, the model can only know this by
// reading it back from Config.
func TestFiltersLineReflectsConfig(t *testing.T) {
	m := newModel(Config{
		Sessions:       testSessions(),
		LiveOnly:       func() bool { return true },
		Cwd:            func() string { return "/keep/repo" },
		TicketPrefixes: []string{"abc", "eng"},
	})

	got := m.filtersLine()
	for _, want := range []string{"--live on", "--cwd /keep/repo", "ticket ABC/ENG"} {
		if !strings.Contains(got, want) {
			t.Errorf("filtersLine() = %q, missing %q", got, want)
		}
	}
}

// With none of --live, --cwd or --ticket-prefix set, the line must say so
// plainly rather than rendering blank fields.
func TestFiltersLineReportsNothingActiveAsOffAndDashes(t *testing.T) {
	got := newModel(Config{Sessions: testSessions()}).filtersLine()

	for _, want := range []string{"--live off", "--cwd —", "ticket —"} {
		if !strings.Contains(got, want) {
			t.Errorf("filtersLine() = %q, missing %q", got, want)
		}
	}
}

// The header names no state directory. One agent's directory is not a property
// of the whole tool, and the picker is gaining providers beyond Claude Code.
// --claude-dir and `agent-sessions config` are where that question is answered.
func TestHeaderBlockNamesNoStateDirectory(t *testing.T) {
	m := sized(t, newModel(Config{Sessions: testSessions(), ClaudeDir: "/opt/claude-config"}), 100, 20)

	if view := m.View(); strings.Contains(view, "/opt/claude-config") {
		t.Errorf("View() still names the state directory:\n%s", view)
	}
	if lines := m.headerLines(); len(lines) != headerBlockLines {
		t.Errorf("headerLines() returned %d lines, want headerBlockLines (%d)", len(lines), headerBlockLines)
	}
}

// The rendered header block, not just the string builders in isolation, has
// to carry the picker's real configuration through to the screen: this is
// what a user actually sees, and what the command layer's wiring into Config
// is ultimately for.
func TestModelViewHeaderReflectsConfig(t *testing.T) {
	m := sized(t, newModel(Config{
		Sessions:       testSessions(),
		Cwd:            func() string { return "/keep/repo" },
		TicketPrefixes: []string{"abc", "eng"},
	}), 100, 20)

	view := m.View()
	for _, want := range []string{"/keep/repo", "ABC/ENG"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() header = missing %q:\n%s", want, view)
		}
	}
}

func TestModelCtrlCQuitsFromEveryMode(t *testing.T) {
	browsing := newModel(Config{
		Sessions: testSessions(),
		Actions: func(session.Session) []action.Action {
			return []action.Action{fakeAction{id: "delete", label: "delete", confirm: "sure?"}}
		},
	})

	next, _ := browsing.Update(keyMsg('/'))
	filtering := next.(model)
	if !filtering.filtering {
		t.Fatal("model.filtering = false, want true after /")
	}

	next, _ = browsing.Update(tea.KeyMsg{Type: tea.KeyEnter})
	previewing := next.(model)
	if !previewing.previewing {
		t.Fatal("model.previewing = false, want true after Enter")
	}

	next, _ = browsing.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	next, _ = next.(model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	confirming := next.(model)
	if !confirming.confirming {
		t.Fatal("model.confirming = false, want true after an action asking to confirm")
	}

	tests := []struct {
		name string
		from model
	}{
		{name: "browsing", from: browsing},
		{name: "filtering", from: filtering},
		{name: "previewing", from: previewing},
		{name: "confirming", from: confirming},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, _ := tt.from.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			got := next.(model)

			if !got.done {
				t.Error("model.done = false, want true after ctrl+c")
			}
			if got.chosen {
				t.Error("model.chosen = true, want ctrl+c to choose nothing")
			}
		})
	}
}

func TestModelConfirmPromptOnlyProceedsOnYes(t *testing.T) {
	tests := []struct {
		name       string
		key        tea.KeyMsg
		wantRan    bool
		wantDone   bool
		wantAsking bool
	}{
		{name: "enter declines", key: tea.KeyMsg{Type: tea.KeyEnter}},
		{name: "y confirms", key: keyMsg('y'), wantRan: true},
		{name: "q quits", key: keyMsg('q'), wantDone: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ran []string
			m := newModel(Config{
				Sessions: testSessions(),
				Actions: func(session.Session) []action.Action {
					return []action.Action{fakeAction{id: "delete", label: "delete", confirm: "sure?", ran: &ran}}
				},
			})
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
			next, _ = next.(model).Update(tea.KeyMsg{Type: tea.KeyEnter})
			asking := next.(model)
			if !asking.confirming {
				t.Fatal("model.confirming = false, want true after an action asking to confirm")
			}

			next, cmd := asking.Update(tt.key)
			got := next.(model)
			// The action itself runs in the Cmd, not in Update: confirming is a
			// keystroke, and an action can reach the network.
			if cmd != nil {
				if msg := cmd(); msg != nil {
					next, _ = got.Update(msg)
					got = next.(model)
				}
			}

			if (len(ran) > 0) != tt.wantRan {
				t.Errorf("action ran = %v, want %v", len(ran) > 0, tt.wantRan)
			}
			if got.done != tt.wantDone {
				t.Errorf("model.done = %v, want %v", got.done, tt.wantDone)
			}
			if got.confirming != tt.wantAsking {
				t.Errorf("model.confirming = %v, want %v", got.confirming, tt.wantAsking)
			}
		})
	}
}

func TestModelFilterBackspaceRemovesAWholeRune(t *testing.T) {
	next, _ := newModel(Config{Sessions: testSessions()}).Update(keyMsg('/'))
	filtering := next.(model)

	for _, r := range "cé" {
		next, _ = filtering.Update(keyMsg(r))
		filtering = next.(model)
	}
	next, _ = filtering.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	filtering = next.(model)

	if filtering.query != "c" {
		t.Errorf("query = %q (% x), want %q", filtering.query, filtering.query, "c")
	}
	if !utf8.ValidString(filtering.query) {
		t.Errorf("query = % x, want valid UTF-8", filtering.query)
	}
}

func TestModelTickCannotIntroduceARowTheFilterExcludes(t *testing.T) {
	now := time.Now()
	kept := []session.Session{{Agent: "claude", ID: "kept", Name: "Kept", Cwd: "/keep/repo", LastActive: now}}
	elsewhere := []session.Session{
		{Agent: "claude", ID: "kept", Name: "Kept", Cwd: "/keep/repo", Live: true, PID: 1, Status: "running", LastActive: now},
		{Agent: "claude", ID: "other", Name: "Other", Cwd: "/other/repo", Live: true, PID: 2, Status: "running", LastActive: now},
	}
	underCwd := func(sessions []session.Session) []session.Session {
		var out []session.Session
		for _, s := range sessions {
			if strings.HasPrefix(s.Cwd, "/keep") {
				out = append(out, s)
			}
		}
		return out
	}

	m := newModel(Config{
		Sessions: kept,
		Refresh:  func() []session.Session { return elsewhere },
		Filter:   underCwd,
	})

	next, _ := m.Update(tickMsg{})
	got := next.(model)

	if len(got.visible()) != 1 || got.visible()[0].ID != "kept" {
		t.Errorf("visible() = %+v, want only the row the filter admits", got.visible())
	}
}

func TestModelTickDropsADemotedRowUnderALiveOnlyFilter(t *testing.T) {
	liveOnly := func(sessions []session.Session) []session.Session {
		var out []session.Session
		for _, s := range sessions {
			if s.Live {
				out = append(out, s)
			}
		}
		return out
	}

	m := newModel(Config{
		Sessions: []session.Session{{Agent: "claude", ID: "live", Name: "Live", Live: true, PID: 1, Status: "running", LastActive: time.Now()}},
		Refresh:  func() []session.Session { return nil },
		Filter:   liveOnly,
	})

	next, _ := m.Update(tickMsg{})
	got := next.(model)

	if len(got.visible()) != 0 {
		t.Errorf("visible() = %+v, want the exited session dropped under --live", got.visible())
	}
}

func TestModelHalfPageKeysMoveWithinTheRowRange(t *testing.T) {
	m := sized(t, newModel(Config{Sessions: manySessions(60)}), 200, 20)
	half := m.halfPage(m.visible())

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	down := next.(model)
	if down.cursor != half {
		t.Errorf("cursor = %d, want %d after ctrl+d from the top", down.cursor, half)
	}
	assertSelectedRowRendered(t, down)

	end := press(t, down, tea.KeyMsg{Type: tea.KeyCtrlD}, 20)
	if end.cursor != 59 {
		t.Errorf("cursor = %d, want 59, ctrl+d must not walk past the last row", end.cursor)
	}
	assertSelectedRowRendered(t, end)

	next, _ = end.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	up := next.(model)
	if up.cursor != 59-half {
		t.Errorf("cursor = %d, want %d after ctrl+u", up.cursor, 59-half)
	}
	assertSelectedRowRendered(t, up)

	top := press(t, up, tea.KeyMsg{Type: tea.KeyCtrlU}, 20)
	if top.cursor != 0 {
		t.Errorf("cursor = %d, want 0, ctrl+u must not walk above the first row", top.cursor)
	}
	assertSelectedRowRendered(t, top)
}

func TestModelPageKeysMoveAWholeWindow(t *testing.T) {
	m := sized(t, newModel(Config{Sessions: manySessions(60)}), 200, 20)
	page := m.rowCapacity(m.visible())

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	down := next.(model)
	if down.cursor != page {
		t.Errorf("cursor = %d, want %d after pgdown", down.cursor, page)
	}
	assertSelectedRowRendered(t, down)

	next, _ = down.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	up := next.(model)
	if up.cursor != 0 {
		t.Errorf("cursor = %d, want 0 after pgup", up.cursor)
	}
	assertSelectedRowRendered(t, up)
}

func TestModelGJumpsToTheLastAndFirstRow(t *testing.T) {
	m := sized(t, newModel(Config{Sessions: manySessions(60)}), 200, 20)

	next, _ := m.Update(keyMsg('G'))
	bottom := next.(model)
	if bottom.cursor != 59 {
		t.Fatalf("cursor = %d, want 59 after G", bottom.cursor)
	}
	assertSelectedRowRendered(t, bottom)

	next, _ = bottom.Update(keyMsg('g'))
	top := next.(model)
	if top.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 after g", top.cursor)
	}
	assertSelectedRowRendered(t, top)

	rendered := renderedRows(top.View())
	if len(rendered) == 0 || !strings.Contains(rendered[0], "row-00") {
		t.Errorf("g left the window scrolled; first rendered row = %q", rendered[0])
	}
}

func TestModelFilteringTypesTheJumpKeysIntoTheQuery(t *testing.T) {
	next, _ := newModel(Config{Sessions: manySessions(60)}).Update(keyMsg('/'))
	filtering := next.(model)

	for _, r := range "gG" {
		next, _ = filtering.Update(keyMsg(r))
		filtering = next.(model)
	}

	if filtering.query != "gG" {
		t.Errorf("query = %q, want the jump keys typed literally while filtering", filtering.query)
	}
}

// Filter splits its query on whitespace, so a row filter that dropped spaces
// could never express the multi-term queries it is built to match.
func TestModelRowFilterAcceptsSpaces(t *testing.T) {
	m := newModel(Config{Sessions: testSessions()})

	next, _ := m.Update(keyMsg('/'))
	filtering := next.(model)
	for _, r := range "fix" {
		next, _ = filtering.Update(keyMsg(r))
		filtering = next.(model)
	}
	next, _ = filtering.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	filtering = next.(model)
	for _, r := range "billing" {
		next, _ = filtering.Update(keyMsg(r))
		filtering = next.(model)
	}

	if filtering.query != "fix billing" {
		t.Fatalf("query = %q, want %q", filtering.query, "fix billing")
	}
	if len(filtering.visible()) != 1 || filtering.visible()[0].ID != "hist" {
		t.Errorf("visible() = %+v, want the row matching both terms", filtering.visible())
	}
}

// The header block is recomputed per frame, so a palette command that toggles
// --live or clears --cwd has to show up there: the rows and the counts follow
// the command's mutation immediately, and a filters line snapshotted at startup
// would sit above a table of nothing but live sessions still claiming --live
// off, or keep naming a cwd filter that no longer applies.
func TestModelViewHeaderFollowsAPaletteCommand(t *testing.T) {
	liveOnly, cwd := false, "/keep/repo"
	cfg := Config{
		Sessions: testSessions(),
		LiveOnly: func() bool { return liveOnly },
		Cwd:      func() string { return cwd },
		Commands: func() []Command {
			return []Command{
				{Label: "toggle --live filter", Run: func() (action.Result, error) {
					liveOnly = !liveOnly
					return action.Result{Status: "toggled"}, nil
				}},
				{Label: "clear --cwd filter", Run: func() (action.Result, error) {
					cwd = ""
					return action.Result{Status: "cleared"}, nil
				}},
			}
		},
	}

	m := sized(t, newModel(cfg), 120, 24)
	for _, want := range []string{"--live off", "/keep/repo"} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("View() header is missing %q before any command ran:\n%s", want, m.View())
		}
	}

	toggled := runPaletteEntry(t, m, 0)
	if !strings.Contains(toggled.View(), "--live on") {
		t.Errorf("View() still reports --live off after the toggle command ran:\n%s", toggled.View())
	}

	cleared := runPaletteEntry(t, toggled, 1)
	if strings.Contains(cleared.View(), "/keep/repo") {
		t.Errorf("View() still reports the cleared --cwd filter:\n%s", cleared.View())
	}
	if !strings.Contains(cleared.View(), "--cwd —") {
		t.Errorf("View() does not report --cwd as unset after the clear command ran:\n%s", cleared.View())
	}
}

func runPaletteEntry(t *testing.T, m model, index int) model {
	t.Helper()
	m = press(t, m, tea.KeyMsg{Type: tea.KeyCtrlP}, 1)
	m = press(t, m, tea.KeyMsg{Type: tea.KeyDown}, index)
	return press(t, m, tea.KeyMsg{Type: tea.KeyEnter}, 1)
}

// The counts line is truncated tail-first inside the header box, so ○ history —
// the one entry present in every frame — comes before the live statuses, which
// vary in both number and length.
func TestCountsLineKeepsHistoryWhenTruncatedAtEightyColumns(t *testing.T) {
	now := time.Now()
	var sessions []session.Session
	for i, status := range []string{"waiting: input needed", "running the test suite", "compacting the transcript", "idle"} {
		sessions = append(sessions, session.Session{Agent: "claude", ID: fmt.Sprintf("l%d", i), Live: true, Status: status, LastActive: now})
	}
	sessions = append(sessions, session.Session{Agent: "claude", ID: "h", LastActive: now.Add(-time.Hour)})

	counts := newModel(Config{Sessions: sessions}).countsLine()
	if history, live := strings.Index(counts, "○ history"), strings.Index(counts, "●"); history > live {
		t.Errorf("countsLine() = %q, want ○ history ahead of the live statuses", counts)
	}

	m := sized(t, newModel(Config{Sessions: sessions}), 80, 24)
	if !strings.Contains(m.View(), "○ history 1") {
		t.Errorf("the header block lost the history count to truncation at 80 columns:\n%s", m.View())
	}
}

// A / query outlives filter mode, where the footer stops showing it, so the
// header block is the only place left that can account for rows the counts line
// still counts but the table no longer shows.
func TestModelViewHeaderSurfacesAnActiveQueryAfterLeavingFilterMode(t *testing.T) {
	m := sized(t, newModel(Config{Sessions: testSessions()}), 120, 24)
	m = press(t, m, keyMsg('/'), 1)
	for _, r := range "search ranking" {
		m = press(t, m, keyMsg(r), 1)
	}
	m = press(t, m, tea.KeyMsg{Type: tea.KeyEsc}, 1)

	if m.filtering {
		t.Fatal("model.filtering = true, want esc to leave filter mode")
	}
	if m.query != "search ranking" {
		t.Fatalf("query = %q, want it to outlive filter mode", m.query)
	}

	view := m.View()
	if !strings.Contains(view, "query search ranking") {
		t.Errorf("View() does not surface the active query anywhere:\n%s", view)
	}
	if !strings.Contains(view, "sessions 2") {
		t.Errorf("View() counts line no longer reports every loaded session:\n%s", view)
	}
}

// An empty list still spends a line saying so, which is a line the frame has to
// have reserved: at these heights that line is the only content there is.
func TestModelViewEmptyListFitsEveryHeight(t *testing.T) {
	for _, height := range []int{1, 2, 3, 4, 5, 6, 9, 10, 24} {
		for _, loadErr := range []error{nil, errors.New("boom")} {
			t.Run(fmt.Sprintf("height=%d/err=%v", height, loadErr != nil), func(t *testing.T) {
				m := sized(t, newModel(Config{LoadErr: loadErr}), 80, height)

				view := m.View()
				if lines := len(strings.Split(view, "\n")); lines > height {
					t.Errorf("View() rendered %d lines, want at most %d:\n%s", lines, height, view)
				}
				if !strings.Contains(view, "no sessions matched") {
					t.Errorf("View() dropped the empty-state line it reserved room for:\n%s", view)
				}
			})
		}
	}
}

// The cursor's row is the last thing a short terminal gives up — before it, the
// header block, the column header, the blank line above the footer, the load
// error and the footer itself — because the rows are what the picker exists to
// show and a frame taller than the terminal loses its top lines silently.
func TestModelViewKeepsTheCursorRowAtEveryHeight(t *testing.T) {
	sessions := manySessions(30)
	sessions[0].Live, sessions[0].Status = true, "running"

	for _, height := range []int{1, 2, 3, 4, 5, 6, 8, 12} {
		t.Run(fmt.Sprintf("height=%d", height), func(t *testing.T) {
			m := sized(t, newModel(Config{Sessions: sessions, LoadErr: errors.New("boom")}), 80, height)

			view := m.View()
			if lines := len(strings.Split(view, "\n")); lines > height {
				t.Errorf("View() rendered %d lines, want at most %d:\n%s", lines, height, view)
			}
			assertSelectedRowRendered(t, m)
		})
	}
}

// The frame invariant, swept: no line wider than the terminal, no frame taller
// than it. Both failures are silent — the renderer drops lines off the top of a
// too-tall frame, and a line past the right edge wraps into a second one, which
// makes the frame too tall in turn. The widths below 48 are the point: that is
// where the gutter, the three fixed columns and the five gaps have already
// claimed the whole terminal, so the rows and the column header can only be cut,
// and a 40-column pane is what a vertical split of an 80-column window gives.
func TestModelViewFrameFitsEveryTerminalSize(t *testing.T) {
	now := time.Now()
	live := session.Session{Agent: "claude", ID: "live", Name: "webapp-8e", Cwd: "/Users/dev/git/webapp", GitBranch: "eng-3120", Live: true, PID: 7, Status: "waiting: input needed", LastActive: now}
	history := session.Session{Agent: "claude", ID: "hist", Name: "Fix search ranking", Cwd: "/Users/dev/git/billing", GitBranch: "eng-3140", LastActive: now.Add(-time.Hour)}

	mixed := manySessions(30)
	for i := range 4 {
		mixed[i].Live, mixed[i].PID, mixed[i].Status = true, 100+i, "waiting: input needed"
	}

	datasets := []struct {
		name     string
		sessions []session.Session
	}{
		{name: "empty"},
		{name: "one live", sessions: []session.Session{live}},
		{name: "one history", sessions: []session.Session{history}},
		{name: "thirty mixed", sessions: mixed},
		{name: "cjk name", sessions: []session.Session{{Agent: "claude", ID: "cjk", Name: "日本語のセッション名です", Cwd: "/Users/dev/git/webapp", GitBranch: "eng-1", Live: true, PID: 9, Status: "running", LastActive: now}}},
		{name: "emoji name", sessions: []session.Session{{Agent: "claude", ID: "emo", Name: "🚀🚀🚀 fix the thing 🚀🚀🚀", Cwd: "/Users/dev/git/webapp", LastActive: now.Add(-90 * 24 * time.Hour)}}},
		{name: "very long branch", sessions: []session.Session{{Agent: "claude", ID: "br", Name: "long branch", Cwd: "/Users/dev/git/webapp", GitBranch: "worktree/eng-3120-a-branch-name-nobody-should-ever-have-typed-but-here-we-are", LastActive: now}}},
		{name: "no branch and no cwd", sessions: []session.Session{{Agent: "claude", ID: "bare", Name: "bare", LastActive: time.Time{}}}},
	}

	for _, ds := range datasets {
		for _, width := range []int{1, 5, 20, 40, 47, 48, 80, 120, 150, 200} {
			t.Run(fmt.Sprintf("%s/width=%d", ds.name, width), func(t *testing.T) {
				for height := 1; height <= 30; height++ {
					for _, key := range []rune{'g', 'G'} {
						m := sized(t, newModel(Config{Sessions: ds.sessions, LoadErr: errors.New("boom")}), width, height)
						m = press(t, m, keyMsg(key), 1)

						view := m.View()
						if lines := len(strings.Split(view, "\n")); lines > height {
							t.Fatalf("height=%d cursor=%c: View() rendered %d lines:\n%s", height, key, lines, view)
						}
						for _, line := range strings.Split(view, "\n") {
							if got := lipgloss.Width(line); got > width {
								t.Fatalf("height=%d cursor=%c: line is %d cells wide, past the %d-column terminal: %q", height, key, got, width, line)
							}
						}
					}
				}
			})
		}
	}
}

// TestPaletteViewFitsEveryHeightAndWidth covers the widths a terminal is
// plausibly used at; this one covers the widths narrower than the palette's own
// furniture, where the four cells its cursor marker spends and the four the
// empty-state line spends on nothing at all are already the whole terminal.
func TestPaletteViewFitsTerminalsNarrowerThanItsFurniture(t *testing.T) {
	for _, width := range []int{1, 3, 5, 20, 21} {
		for _, query := range []string{"", "zzz"} {
			t.Run(fmt.Sprintf("width=%d/query=%q", width, query), func(t *testing.T) {
				m := sized(t, newModel(Config{Sessions: testSessions(), Actions: func(session.Session) []action.Action {
					return []action.Action{fakeAction{id: "copy", label: "copy resume command for a session with a long name"}}
				}}), width, 24)
				next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
				m = next.(model)
				for _, r := range query {
					next, _ = m.Update(keyMsg(r))
					m = next.(model)
				}

				for _, line := range strings.Split(m.View(), "\n") {
					if got := lipgloss.Width(line); got > width {
						t.Errorf("palette line is %d cells wide, past the %d-column terminal: %q", got, width, line)
					}
				}
			})
		}
	}
}

// ctrl+h and the palette's "toggle --live filter" must be the same operation.
// The rows are drawn through a filter closure that reads the flag live, so the
// key has to go through the reload path too or the list keeps showing what it
// showed before.
func TestModelCtrlHTogglesTheLiveFilterAndReloads(t *testing.T) {
	liveOnly := false
	reloaded := 0

	m := sized(t, newModel(Config{
		Sessions: testSessions(),
		ToggleLive: func() (action.Result, error) {
			liveOnly = !liveOnly
			return action.Result{Status: fmt.Sprintf("--live %v", liveOnly)}, nil
		},
		Reload: func() ([]session.Session, error) {
			reloaded++
			return testSessions(), nil
		},
		LiveOnly: func() bool { return liveOnly },
	}), 100, 24)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	after := next.(model)

	if !liveOnly {
		t.Error("ctrl+h did not flip the --live filter")
	}
	if reloaded == 0 {
		t.Error("ctrl+h did not reload the list, so the filter change stays invisible")
	}
	if !strings.Contains(after.View(), "--live true") {
		t.Errorf("View() does not report the new filter state:\n%s", after.View())
	}

	next, _ = after.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	if liveOnly {
		t.Error("ctrl+h does not toggle back off")
	}
	if !strings.Contains(next.(model).View(), "--live off") {
		t.Errorf("the header block still names the old filter:\n%s", next.(model).View())
	}
}

func TestModelCtrlHWithoutAToggleSaysSo(t *testing.T) {
	m := sized(t, newModel(Config{Sessions: testSessions()}), 100, 24)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlH})
	if cmd != nil {
		t.Error("ctrl+h dispatched a Cmd with no toggle wired")
	}
	if !strings.Contains(next.(model).View(), "unavailable") {
		t.Errorf("View() does not say the filter is unavailable:\n%s", next.(model).View())
	}
}

// Every browsing key must appear in the panel. One left out reads as a key that
// does not exist, which is worse than having no panel at all.
func TestHelpPanelListsEveryBrowsingKey(t *testing.T) {
	m := sized(t, newModel(Config{Sessions: manySessions(20)}), 110, 30)
	next, _ := m.Update(keyMsg('?'))
	view := next.(model).View()

	for _, b := range helpBindings {
		if !strings.Contains(view, b.key) {
			t.Errorf("the help panel omits the %q key:\n%s", b.key, view)
		}
		if !strings.Contains(view, b.what) {
			t.Errorf("the help panel omits %q, the description of %q:\n%s", b.what, b.key, view)
		}
	}
}

func TestHelpPanelTogglesOff(t *testing.T) {
	m := sized(t, newModel(Config{Sessions: manySessions(20)}), 110, 30)

	next, _ := m.Update(keyMsg('?'))
	if !next.(model).help {
		t.Fatal("? did not open the help panel")
	}
	if !strings.Contains(next.(model).View(), "keys") {
		t.Fatalf("the panel is not drawn:\n%s", next.(model).View())
	}

	next, _ = next.(model).Update(keyMsg('?'))
	if next.(model).help {
		t.Error("? did not close the help panel")
	}
	if strings.Contains(next.(model).View(), "─ keys") {
		t.Errorf("the panel is still drawn after closing:\n%s", next.(model).View())
	}
}

// The panel costs the rows several lines, so a frame that forgets to count them
// overflows — and bubbletea drops the overflow off the top, taking the header
// and the cursor with it.
func TestHelpPanelViewFitsTheTerminalHeight(t *testing.T) {
	for _, height := range []int{4, 8, 12, 18, 24, 40} {
		t.Run(fmt.Sprintf("height=%d", height), func(t *testing.T) {
			for _, width := range []int{40, 80, 110, 200} {
				m := sized(t, newModel(Config{Sessions: manySessions(60)}), width, height)
				next, _ := m.Update(keyMsg('?'))
				view := next.(model).View()

				lines := strings.Split(view, "\n")
				if len(lines) > height {
					t.Errorf("width=%d: View() rendered %d lines in a %d-line terminal:\n%s", width, len(lines), height, view)
				}
				for _, line := range lines {
					if got := lipgloss.Width(line); got > width {
						t.Errorf("width=%d: line is %d cells: %q", width, got, line)
					}
				}
			}
		})
	}
}

// The panel is asked for explicitly, so it outranks the wordmark above it — but
// never the rows it exists to explain how to move through.
func TestHelpPanelIsGivenUpBeforeTheRows(t *testing.T) {
	m := sized(t, newModel(Config{Sessions: manySessions(60)}), 110, 6)
	next, _ := m.Update(keyMsg('?'))
	tight := next.(model)

	if len(renderedRows(tight.View())) == 0 {
		t.Errorf("View() dropped every row to fit the help panel:\n%s", tight.View())
	}

	roomy := sized(t, newModel(Config{Sessions: manySessions(60)}), 110, 30)
	next, _ = roomy.Update(keyMsg('?'))
	open := next.(model)
	if strings.Contains(open.View(), wordmarkGlyphTop) && !strings.Contains(open.View(), "─ keys") {
		t.Errorf("the panel was dropped while the wordmark survived:\n%s", open.View())
	}
}

// The footer belongs on the terminal's last line whatever the list length.
// Filtering 156 rows down to four used to carry it up with them, leaving it
// floating in the middle of the screen with blank space below.
func TestModelViewKeepsTheFooterOnTheLastLineAtEveryListLength(t *testing.T) {
	for _, count := range []int{0, 1, 2, 4, 7, 20, 60} {
		t.Run(fmt.Sprintf("sessions=%d", count), func(t *testing.T) {
			for _, height := range []int{14, 20, 24, 40} {
				m := sized(t, newModel(Config{Sessions: manySessions(count)}), 110, height)
				lines := strings.Split(m.View(), "\n")

				if len(lines) != height {
					t.Errorf("height=%d: View() rendered %d lines, want the frame to fill the terminal", height, len(lines))
				}
				if last := lines[len(lines)-1]; !strings.Contains(last, "preview") {
					t.Errorf("height=%d: the last line is %q, want the footer", height, last)
				}
			}
		})
	}
}

// A live/history mix reserves a line for the rule between them, so the row
// area's own height is one more than the rows it holds. Padding to the wrong
// one of the two leaves the footer a line off the bottom.
func TestModelViewFillsTheTerminalWithADividerReserved(t *testing.T) {
	for _, height := range []int{14, 20, 30} {
		m := sized(t, newModel(Config{Sessions: testSessions()}), 110, height)
		lines := strings.Split(m.View(), "\n")

		if len(lines) != height {
			t.Errorf("height=%d: View() rendered %d lines, want %d", height, len(lines), height)
		}
		if !strings.Contains(m.View(), "─────") {
			t.Errorf("height=%d: the live/history rule is missing, so this fixture is not exercising the reservation", height)
		}
	}
}

// The help panel takes its lines from the rows, not from the footer's place at
// the bottom.
func TestHelpPanelDoesNotMoveTheFooterOffTheLastLine(t *testing.T) {
	for _, count := range []int{2, 5, 60} {
		m := sized(t, newModel(Config{Sessions: manySessions(count)}), 110, 24)
		next, _ := m.Update(keyMsg('?'))
		lines := strings.Split(next.(model).View(), "\n")

		if len(lines) != 24 {
			t.Errorf("sessions=%d: View() rendered %d lines with the panel open, want 24", count, len(lines))
		}
		if last := lines[len(lines)-1]; !strings.Contains(last, "preview") {
			t.Errorf("sessions=%d: the last line is %q, want the footer", count, last)
		}
	}
}

// The reported failure: scrolling onto a row whose name is a pasted brief blew
// the frame far past the terminal, and bubbletea drops the overflow off the top,
// so the header, the column header and the cursor all vanished. Scrolling the
// whole list is what exercises it, since the row has to be inside the window.
func TestModelViewSurvivesScrollingThroughMultiLineNames(t *testing.T) {
	brief := "Apply security upgrades in this repository\n\nRules:\n- Edit files only.\n- Do not commit.\n\nYour final message must be a single JSON object\n{\n  \"summary\": \"one line\"\n}\n"

	sessions := manySessions(30)
	for i := range sessions {
		if i%3 == 0 {
			sessions[i].Name = brief
		}
	}

	const height = 20
	m := sized(t, newModel(Config{Sessions: sessions}), 110, height)

	for step := range len(sessions) + 5 {
		view := m.View()
		lines := strings.Split(view, "\n")
		if len(lines) != height {
			t.Fatalf("step %d: View() rendered %d lines in a %d-line terminal:\n%s", step, len(lines), height, view)
		}
		if !strings.Contains(lines[len(lines)-1], "preview") {
			t.Fatalf("step %d: the footer is not on the last line: %q", step, lines[len(lines)-1])
		}
		if !strings.Contains(view, "AGENT") {
			t.Fatalf("step %d: the column header was pushed off the frame:\n%s", step, view)
		}
		next, _ := m.Update(keyMsg('j'))
		m = next.(model)
	}
}

// The same name reaches the preview's header box and the palette's title.
func TestPreviewAndPaletteSurviveAMultiLineName(t *testing.T) {
	brief := "Apply security upgrades\n\nRules:\n- Edit files only.\n- Do not commit.\n"
	sessions := []session.Session{{Agent: "claude", ID: "s1", Name: brief, Cwd: "/Users/dev/x", GitBranch: "b", LastActive: time.Now()}}

	const height = 20
	m := sized(t, newModel(Config{
		Sessions: sessions,
		Preview: func(session.Session) ([]session.Message, error) {
			return []session.Message{{Role: "user", Text: "hi"}}, nil
		},
		Actions:  func(session.Session) []action.Action { return nil },
		Commands: func() []Command { return nil },
	}), 110, height)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(cmd())
	if got := len(strings.Split(next.(model).View(), "\n")); got != height {
		t.Errorf("the preview rendered %d lines in a %d-line terminal:\n%s", got, height, next.(model).View())
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if got := len(strings.Split(next.(model).View(), "\n")); got > height {
		t.Errorf("the palette rendered %d lines in a %d-line terminal:\n%s", got, height, next.(model).View())
	}
}

// A background agent's progress line is a sentence and the status column is two
// dozen cells, so the footer lends the cursor's row one full-width line. The
// keys it displaces are a keystroke away under ?.
func TestFooterShowsTheCursorRowsProgressLine(t *testing.T) {
	rows := []session.Session{
		{Agent: "claude", ID: "job", Name: "job row", JobState: "working", Detail: "Verifying all 5 fixed cases across 4 runs", LastActive: time.Now()},
		{Agent: "claude", ID: "plain", Name: "plain row", LastActive: time.Now().Add(-time.Hour)},
	}
	m := sized(t, newModel(Config{Sessions: rows}), 100, 20)

	onJob := m.View()
	if !strings.Contains(onJob, "Verifying all 5 fixed cases") {
		t.Errorf("the footer does not show the cursor row's progress line:\n%s", onJob)
	}

	next, _ := m.Update(keyMsg('j'))
	onPlain := next.(model).View()
	if strings.Contains(onPlain, "Verifying all 5 fixed cases") {
		t.Errorf("the footer kept the previous row's progress line:\n%s", onPlain)
	}
	if !strings.Contains(onPlain, keyHints) {
		t.Errorf("a row with no progress line did not get the key hints back:\n%s", onPlain)
	}
}

// A progress line is passive information; a status, a filter and a confirmation
// are all things the owner is in the middle of.
func TestFooterProgressLineYieldsToEverythingElse(t *testing.T) {
	rows := []session.Session{{Agent: "claude", ID: "job", Name: "job row", Detail: "Verifying all 5 fixed cases", LastActive: time.Now()}}
	base := sized(t, newModel(Config{Sessions: rows}), 100, 20)

	for _, tt := range []struct {
		name string
		mut  func(model) model
		want string
	}{
		{"status", func(m model) model { m.status = "opened PR #6 (open)"; return m }, "opened PR #6"},
		{"filter", func(m model) model { m.filtering = true; m.query = "eng"; return m }, "filter: eng"},
		{"confirm", func(m model) model { m.confirming = true; m.confirmPrompt = "delete?"; return m }, "delete?"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.mut(base)
			view := m.View()
			if !strings.Contains(view, tt.want) {
				t.Errorf("View() does not show %q:\n%s", tt.want, view)
			}
			if strings.Contains(view, "Verifying all 5 fixed cases") {
				t.Errorf("the progress line displaced the %s:\n%s", tt.name, view)
			}
		})
	}
}
