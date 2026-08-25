package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/eduardvoiculescu/agent-sessions/internal/action"
	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

type fakeAction struct {
	id      string
	label   string
	confirm string
	group   action.Group
	ran     *[]string
	result  action.Result
	err     error
}

func (f fakeAction) ID() string                     { return f.id }
func (f fakeAction) Group() action.Group            { return f.group }
func (f fakeAction) Label(session.Session) string   { return f.label }
func (f fakeAction) Available(session.Session) bool { return true }
func (f fakeAction) Confirm(session.Session) string { return f.confirm }

func (f fakeAction) Run(_ context.Context, s session.Session) (action.Result, error) {
	if f.ran != nil {
		*f.ran = append(*f.ran, f.id+":"+s.ID)
	}
	return f.result, f.err
}

func paletteRows() []session.Session {
	now := time.Now()
	return []session.Session{
		{Agent: "claude", ID: "alpha", Name: "alpha", Live: true, PID: 1, Status: "running", LastActive: now},
		{Agent: "claude", ID: "beta", Name: "beta", LastActive: now.Add(-time.Hour)},
	}
}

func paletteModel(t *testing.T, actions []action.Action, ran *[]string) model {
	t.Helper()
	m := newModel(Config{
		Sessions: paletteRows(),
		Actions:  func(session.Session) []action.Action { return actions },
	})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	return next.(model)
}

// feed drives the model the way the runtime does, resolving the Cmd a keystroke
// returns. Action runs are asynchronous — an action can reach the network — so a
// test that presses enter and reads the model straight away would see only
// "working…". Only a key's Cmd is resolved: a tickMsg returns tea.Tick, whose
// Cmd sleeps for a whole second before it yields anything.
func feed(m model, keys ...tea.Msg) model {
	for _, k := range keys {
		next, cmd := m.Update(k)
		m = next.(model)
		if _, isKey := k.(tea.KeyMsg); !isKey || cmd == nil {
			continue
		}
		if msg := cmd(); msg != nil {
			next, _ = m.Update(msg)
			m = next.(model)
		}
	}
	return m
}

func runes(s string) []tea.Msg {
	out := make([]tea.Msg, 0, len(s))
	for _, r := range s {
		out = append(out, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return out
}

var ctrlP = tea.KeyMsg{Type: tea.KeyCtrlP}

func TestCtrlPOpensPaletteForTheHighlightedSession(t *testing.T) {
	m := feed(paletteModel(t, []action.Action{fakeAction{id: "a", label: "alpha action"}}, nil), ctrlP)

	if !m.palette {
		t.Fatal("model.palette = false after ctrl+p")
	}
	if m.paletteTarget.ID != "alpha" {
		t.Errorf("paletteTarget.ID = %q, want the highlighted row %q", m.paletteTarget.ID, "alpha")
	}
	if !strings.Contains(m.View(), "alpha action") {
		t.Errorf("View() does not show the action:\n%s", m.View())
	}
}

func TestPaletteRunsActionAgainstTheTargetSession(t *testing.T) {
	var ran []string
	m := feed(paletteModel(t, []action.Action{fakeAction{id: "copy", label: "copy id", ran: &ran, result: action.Result{Status: "copied"}}}, &ran),
		ctrlP, tea.KeyMsg{Type: tea.KeyEnter})

	if len(ran) != 1 || ran[0] != "copy:alpha" {
		t.Fatalf("ran = %v, want [copy:alpha]", ran)
	}
	if m.palette {
		t.Error("palette stayed open after running an action")
	}
	if m.status != "copied" {
		t.Errorf("status = %q, want the action's Result.Status", m.status)
	}
}

func TestPaletteFilterNarrowsActions(t *testing.T) {
	actions := []action.Action{
		fakeAction{id: "kill", label: "kill process (pid 1)"},
		fakeAction{id: "copy", label: "copy session id"},
	}
	m := feed(paletteModel(t, actions, nil), append([]tea.Msg{ctrlP}, runes("kill")...)...)

	view := m.View()
	if !strings.Contains(view, "kill process") {
		t.Errorf("View() lost the matching action:\n%s", view)
	}
	if strings.Contains(view, "copy session id") {
		t.Errorf("View() still shows a non-matching action:\n%s", view)
	}
}

func TestPaletteEscClosesWithoutRunning(t *testing.T) {
	var ran []string
	m := feed(paletteModel(t, []action.Action{fakeAction{id: "delete", label: "delete", ran: &ran}}, &ran),
		ctrlP, tea.KeyMsg{Type: tea.KeyEsc})

	if m.palette {
		t.Error("palette still open after esc")
	}
	if len(ran) != 0 {
		t.Errorf("ran = %v, want nothing run", ran)
	}
}

func TestPaletteConfirmationBlocksUntilAccepted(t *testing.T) {
	var ran []string
	base := paletteModel(t, []action.Action{fakeAction{id: "delete", label: "delete session", confirm: "Delete alpha?", ran: &ran}}, &ran)

	asking := feed(base, ctrlP, tea.KeyMsg{Type: tea.KeyEnter})
	if len(ran) != 0 {
		t.Fatalf("action ran before confirmation: %v", ran)
	}
	if !strings.Contains(asking.View(), "Delete alpha?") {
		t.Errorf("View() does not show the confirmation:\n%s", asking.View())
	}

	declined := feed(asking, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if len(ran) != 0 {
		t.Errorf("action ran after declining: %v", ran)
	}
	if declined.palette {
		t.Error("palette reopened after declining")
	}

	accepted := feed(asking, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(ran) != 1 || ran[0] != "delete:alpha" {
		t.Errorf("ran = %v, want [delete:alpha]", ran)
	}
	if accepted.status == "" {
		t.Error("status is empty after a confirmed action")
	}
}

func TestPaletteTickCannotRetargetTheAction(t *testing.T) {
	var ran []string
	// The refresh makes beta the most recently active live session, so a sort
	// during the palette would move it under the cursor.
	m := newModel(Config{
		Sessions: paletteRows(),
		Actions: func(session.Session) []action.Action {
			return []action.Action{fakeAction{id: "delete", label: "delete", confirm: "sure?", ran: &ran}}
		},
		Refresh: func() []session.Session {
			return []session.Session{{Agent: "claude", ID: "beta", Name: "beta", Live: true, PID: 2, Status: "running", LastActive: time.Now().Add(time.Minute)}}
		},
	})
	m = feed(m, tea.WindowSizeMsg{Width: 120, Height: 24}, ctrlP, tea.KeyMsg{Type: tea.KeyEnter},
		tickMsg{}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	if len(ran) != 1 || ran[0] != "delete:alpha" {
		t.Errorf("ran = %v, want [delete:alpha] — the session the palette was opened for", ran)
	}
}

func TestPaletteActionErrorIsShownAndListSurvives(t *testing.T) {
	m := feed(paletteModel(t, []action.Action{fakeAction{id: "kill", label: "kill", err: errors.New("pid 1 is no longer running")}}, nil), ctrlP)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Update() returned no Cmd, want the action handed to one rather than run inside Update")
	}
	next, followUp := next.(model).Update(cmd())
	got := next.(model)

	if !strings.Contains(got.View(), "no longer running") {
		t.Errorf("View() does not surface the action error:\n%s", got.View())
	}
	if len(got.visible()) != 2 {
		t.Errorf("visible() = %d rows, want the list intact after a failed action", len(got.visible()))
	}
	// A regression here would exit the picker on any action failure, silently
	// breaking "no action failure may exit the picker" while every prior
	// assertion above kept passing.
	if got.done {
		t.Error("model.done = true, want a failed action to leave the picker running")
	}
	if followUp != nil {
		t.Error("handling the action result returned a non-nil Cmd, want nil so a failed action cannot quit the program")
	}
}

func TestPaletteRefreshReloadsSessions(t *testing.T) {
	reloaded := []session.Session{{Agent: "claude", ID: "only", Name: "only", LastActive: time.Now()}}
	m := newModel(Config{
		Sessions: paletteRows(),
		Actions: func(session.Session) []action.Action {
			return []action.Action{fakeAction{id: "delete", label: "delete", result: action.Result{Status: "trashed", Refresh: true}}}
		},
		Reload: func() ([]session.Session, error) { return reloaded, nil },
	})
	m = feed(m, tea.WindowSizeMsg{Width: 120, Height: 24}, ctrlP, tea.KeyMsg{Type: tea.KeyEnter})

	if len(m.visible()) != 1 || m.visible()[0].ID != "only" {
		t.Errorf("visible() = %+v, want the reloaded list", m.visible())
	}
}

func TestPaletteIncludesGlobalCommands(t *testing.T) {
	toggled := false
	m := newModel(Config{
		Sessions: paletteRows(),
		Actions:  func(session.Session) []action.Action { return nil },
		Commands: func() []Command {
			return []Command{{Label: "toggle --live", Run: func() (action.Result, error) {
				toggled = true
				return action.Result{Status: "live only"}, nil
			}}}
		},
	})
	m = feed(m, tea.WindowSizeMsg{Width: 120, Height: 24}, ctrlP, tea.KeyMsg{Type: tea.KeyEnter})

	if !toggled {
		t.Error("global command did not run")
	}
	if m.status != "live only" {
		t.Errorf("status = %q, want the command's status", m.status)
	}
}

func TestPaletteCommandErrorIsShownAndListSurvives(t *testing.T) {
	m := newModel(Config{
		Sessions: paletteRows(),
		Actions:  func(session.Session) []action.Action { return nil },
		Commands: func() []Command {
			return []Command{{Label: "broken command", Run: func() (action.Result, error) {
				return action.Result{}, errors.New("boom")
			}}}
		},
	})
	m = feed(m, tea.WindowSizeMsg{Width: 120, Height: 24}, ctrlP)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)

	if !strings.Contains(got.View(), "boom") {
		t.Errorf("View() does not surface the command error:\n%s", got.View())
	}
	if len(got.visible()) != 2 {
		t.Errorf("visible() = %d rows, want the list intact after a failed command", len(got.visible()))
	}
	if got.done {
		t.Error("model.done = true, want a failed command to leave the picker running")
	}
	if cmd != nil {
		t.Error("Update() returned a non-nil Cmd, want nil so a failed command cannot quit the program")
	}
}

func TestGAndCapitalGTypeIntoThePaletteFilter(t *testing.T) {
	m := feed(paletteModel(t, []action.Action{fakeAction{id: "a", label: "aaa"}}, nil), ctrlP)
	m = feed(m, runes("gG")...)

	if m.paletteQuery != "gG" {
		t.Errorf("paletteQuery = %q, want %q — jump keys must not fire inside the palette", m.paletteQuery, "gG")
	}
}

// TestPaletteCommandsAreReEvaluatedOnEveryOpen guards against Commands being
// captured once: a command that changes the state deciding which commands
// apply (clearing a filter, say) must make its own entry disappear on the
// next open, not linger for the rest of the process.
func TestPaletteCommandsAreReEvaluatedOnEveryOpen(t *testing.T) {
	cwdFilterActive := true
	m := newModel(Config{
		Sessions: paletteRows(),
		Actions:  func(session.Session) []action.Action { return nil },
		Commands: func() []Command {
			cmds := []Command{{Label: "toggle --live", Run: func() (action.Result, error) {
				return action.Result{Status: "toggled"}, nil
			}}}
			if cwdFilterActive {
				cmds = append(cmds, Command{Label: "clear --cwd filter", Run: func() (action.Result, error) {
					cwdFilterActive = false
					return action.Result{Status: "cleared --cwd"}, nil
				}})
			}
			return cmds
		},
	})
	m = feed(m, tea.WindowSizeMsg{Width: 120, Height: 24}, ctrlP)

	if !strings.Contains(m.View(), "clear --cwd filter") {
		t.Fatalf("palette does not offer clear --cwd filter while the cwd filter is active:\n%s", m.View())
	}

	m = feed(m, tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyEnter})
	if m.status != "cleared --cwd" {
		t.Fatalf("status = %q, want the clear-cwd command's status", m.status)
	}

	m = feed(m, ctrlP)
	if strings.Contains(m.View(), "clear --cwd filter") {
		t.Errorf("palette still offers clear --cwd filter on reopen, after it cleared the state that produced it:\n%s", m.View())
	}
}

func manyFakeActions(n int) []action.Action {
	actions := make([]action.Action, n)
	for i := range actions {
		actions[i] = fakeAction{id: fmt.Sprintf("act-%02d", i), label: fmt.Sprintf("action-%02d", i)}
	}
	return actions
}

func paletteEntryLines(view string) []string {
	var lines []string
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "action-") {
			lines = append(lines, line)
		}
	}
	return lines
}

func repeatMsg(n int, msg tea.Msg) []tea.Msg {
	out := make([]tea.Msg, n)
	for i := range out {
		out[i] = msg
	}
	return out
}

func assertSelectedPaletteEntryRendered(t *testing.T, m model) {
	t.Helper()
	entries := m.paletteItems
	want := entries[m.paletteCursor].label
	for _, line := range paletteEntryLines(m.View()) {
		if strings.Contains(line, want) && strings.Contains(line, "▸") {
			return
		}
	}
	t.Errorf("selected entry %q is outside the rendered window (cursor = %d):\n%s", want, m.paletteCursor, m.View())
}

func TestPaletteViewFitsTheTerminalHeight(t *testing.T) {
	m := newModel(Config{
		Sessions: paletteRows(),
		Actions:  func(session.Session) []action.Action { return manyFakeActions(40) },
	})
	m = feed(m, tea.WindowSizeMsg{Width: 120, Height: 20}, ctrlP)

	view := m.View()
	// The whole frame is measured rather than the entry lines alone: group
	// headers and their separators occupy entry lines too, and the frame filling
	// the terminal exactly is the invariant that keeps the renderer from dropping
	// its top.
	if lines := len(strings.Split(view, "\n")); lines != 20 {
		t.Errorf("View() rendered %d lines, want the terminal height 20:\n%s", lines, view)
	}
	if got, capacity := len(paletteEntryLines(view)), m.paletteCapacity(); got > capacity {
		t.Errorf("View() rendered %d action lines, want at most %d", got, capacity)
	}
}

func TestPaletteViewKeepsTheSelectedEntryInsideTheWindow(t *testing.T) {
	m := newModel(Config{
		Sessions: paletteRows(),
		Actions:  func(session.Session) []action.Action { return manyFakeActions(40) },
	})
	m = feed(m, tea.WindowSizeMsg{Width: 120, Height: 20}, ctrlP)

	m = feed(m, repeatMsg(len(m.paletteItems), tea.KeyMsg{Type: tea.KeyDown})...)
	if got, want := m.paletteCursor, lastRunnable(m); got != want {
		t.Fatalf("paletteCursor = %d, want %d at the bottom", got, want)
	}
	assertSelectedPaletteEntryRendered(t, m)

	m = feed(m, repeatMsg(len(m.paletteItems), tea.KeyMsg{Type: tea.KeyUp})...)
	if got, want := m.paletteCursor, firstRunnable(m); got != want {
		t.Fatalf("paletteCursor = %d, want %d back at the top", got, want)
	}
	assertSelectedPaletteEntryRendered(t, m)
}

func TestPaletteFilterResetsTheWindow(t *testing.T) {
	m := newModel(Config{
		Sessions: paletteRows(),
		Actions:  func(session.Session) []action.Action { return manyFakeActions(40) },
	})
	m = feed(m, tea.WindowSizeMsg{Width: 120, Height: 20}, ctrlP)
	m = feed(m, repeatMsg(len(m.paletteItems), tea.KeyMsg{Type: tea.KeyDown})...)
	if got, want := m.paletteCursor, lastRunnable(m); got != want {
		t.Fatalf("paletteCursor = %d, want %d before filtering", got, want)
	}

	m = feed(m, runes("action-01")...)
	if runnableCount(m) != 1 {
		t.Fatalf("paletteItems = %+v, want exactly the one matching entry", m.paletteItems)
	}
	if m.paletteCursor != firstRunnable(m) {
		t.Errorf("paletteCursor = %d, want 0 after filtering shrinks the list out from under it", m.paletteCursor)
	}
	assertSelectedPaletteEntryRendered(t, m)
}

// TestPaletteEntryVanishingCannotRetargetTheAction is the entry-list half of
// TestPaletteTickCannotRetargetTheAction: the cursor sits on kill process, the
// process exits before the keypress is handled, and delete session must not
// slide into the slot the user was aiming at.
func TestPaletteEntryVanishingCannotRetargetTheAction(t *testing.T) {
	var ran []string
	killable := true
	kill := fakeAction{id: "kill", label: "kill process (pid 1)", ran: &ran}
	del := fakeAction{id: "delete", label: "delete session", confirm: "Move alpha's transcript to trash?", ran: &ran}
	m := newModel(Config{
		Sessions: paletteRows(),
		Actions: func(session.Session) []action.Action {
			if killable {
				return []action.Action{kill, del}
			}
			return []action.Action{del}
		},
	})
	m = feed(m, tea.WindowSizeMsg{Width: 120, Height: 24}, ctrlP)

	if !strings.Contains(m.View(), "kill process (pid 1)") {
		t.Fatalf("palette does not offer the kill entry the user is about to press:\n%s", m.View())
	}
	killable = false
	m = feed(m, tea.KeyMsg{Type: tea.KeyEnter})

	if len(ran) != 1 || ran[0] != "kill:alpha" {
		t.Errorf("ran = %v, want [kill:alpha] — the entry the cursor was on", ran)
	}
	if m.confirming {
		t.Errorf("confirmation %q appeared; the cursor was on kill process, not delete session", m.confirmPrompt)
	}
}

func TestCtrlPOpensThePaletteWhileTheRowFilterIsOpen(t *testing.T) {
	var ran []string
	m := paletteModel(t, []action.Action{fakeAction{id: "copy", label: "copy session id", ran: &ran, result: action.Result{Status: "copied"}}}, &ran)
	m = feed(m, keyMsg('/'))
	m = feed(m, runes("alpha")...)
	if !m.filtering {
		t.Fatal("model.filtering = false after /")
	}

	m = feed(m, ctrlP)
	if !m.palette {
		t.Fatalf("ctrl+p did not open the palette from the filtering mode:\n%s", m.View())
	}
	if m.paletteTarget.ID != "alpha" {
		t.Errorf("paletteTarget.ID = %q, want the row the filter left highlighted", m.paletteTarget.ID)
	}

	m = feed(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(ran) != 1 || ran[0] != "copy:alpha" {
		t.Fatalf("ran = %v, want [copy:alpha]", ran)
	}
	if !strings.Contains(m.View(), "copied") {
		t.Errorf("View() hides the action's status behind the filter line:\n%s", m.View())
	}
}

func TestPaletteSpaceTypesOneSpace(t *testing.T) {
	actions := []action.Action{
		fakeAction{id: "copy.id", label: "copy session id"},
		fakeAction{id: "copy.cwd", label: "copy working directory"},
	}
	m := feed(paletteModel(t, actions, nil), ctrlP)
	m = feed(m, runes("copy")...)
	m = feed(m, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = feed(m, runes("id")...)

	if m.paletteQuery != "copy id" {
		t.Errorf("paletteQuery = %q, want %q — one space per keypress", m.paletteQuery, "copy id")
	}
	if got := paletteLabels(m); len(got) != 1 || got[0] != "copy session id" {
		t.Errorf("paletteItems = %v, want only the entry matching both terms", got)
	}

	m = feed(m, tea.KeyMsg{Type: tea.KeyBackspace}, tea.KeyMsg{Type: tea.KeyBackspace}, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.paletteQuery != "copy" {
		t.Errorf("paletteQuery = %q, want %q — one backspace per space", m.paletteQuery, "copy")
	}
	// The query string alone proves nothing: the entries Enter runs from are the
	// snapshot, so a backspace that does not rebuild it leaves the widened query
	// displayed over the narrow list it replaced.
	if got := paletteLabels(m); len(got) != 2 {
		t.Errorf("paletteItems = %v, want both copy entries back once the filter widened", got)
	}
}

// firstRunnable and lastRunnable are where the cursor can actually rest. Group
// headers and their separators are entries too, so a raw index is no longer the
// same thing as a position in the list.
func firstRunnable(m model) int {
	for i, e := range m.paletteItems {
		if e.runnable() {
			return i
		}
	}
	return -1
}

func lastRunnable(m model) int {
	for i := len(m.paletteItems) - 1; i >= 0; i-- {
		if m.paletteItems[i].runnable() {
			return i
		}
	}
	return -1
}

func runnableCount(m model) int {
	n := 0
	for _, e := range m.paletteItems {
		if e.runnable() {
			n++
		}
	}
	return n
}

// paletteLabels lists what the palette can actually run, skipping the group
// headers and their separators.
func paletteLabels(m model) []string {
	out := make([]string, 0, len(m.paletteItems))
	for _, e := range m.paletteItems {
		if e.runnable() {
			out = append(out, e.label)
		}
	}
	return out
}

func TestReloadKeepsTheActionStatusAndReportsTheErrorSeparately(t *testing.T) {
	survivor := []session.Session{{Agent: "claude", ID: "hist", Name: "Fix search ranking", LastActive: time.Now()}}
	m := newModel(Config{
		Sessions: testSessions(),
		LoadErr:  errors.New("1 of 3 transcripts could not be read"),
		Actions: func(session.Session) []action.Action {
			return []action.Action{fakeAction{id: "delete", label: "delete", result: action.Result{Status: "trashed live.jsonl", Refresh: true}}}
		},
		Reload: func() ([]session.Session, error) {
			return survivor, errors.New("1 of 3 transcripts could not be read")
		},
	})
	m = feed(m, tea.WindowSizeMsg{Width: 120, Height: 24}, ctrlP, tea.KeyMsg{Type: tea.KeyEnter})

	if m.status != "trashed live.jsonl" {
		t.Errorf("status = %q, want the delete's own status; a partial load error must not read as the delete having failed", m.status)
	}
	if len(m.visible()) != 1 || m.visible()[0].ID != "hist" {
		t.Errorf("visible() = %+v, want the reloaded list with the deleted row gone", m.visible())
	}
	if m.loadErr == nil {
		t.Error("loadErr = nil, want the partial load failure still reported on its own line")
	}
}

func TestReloadClearsAStaleLoadError(t *testing.T) {
	reloaded := []session.Session{{Agent: "claude", ID: "hist", Name: "Fix search ranking", LastActive: time.Now()}}
	m := newModel(Config{
		Sessions: testSessions(),
		LoadErr:  errors.New("1 of 3 transcripts could not be read"),
		Actions: func(session.Session) []action.Action {
			return []action.Action{fakeAction{id: "delete", label: "delete", result: action.Result{Status: "trashed", Refresh: true}}}
		},
		Reload: func() ([]session.Session, error) { return reloaded, nil },
	})
	m = feed(m, tea.WindowSizeMsg{Width: 120, Height: 24}, ctrlP, tea.KeyMsg{Type: tea.KeyEnter})

	if m.loadErr != nil {
		t.Errorf("loadErr = %v, want the initial load's error cleared by a clean reload", m.loadErr)
	}
}

func TestReloadThatYieldsNothingKeepsTheListStanding(t *testing.T) {
	m := newModel(Config{
		Sessions: testSessions(),
		Actions: func(session.Session) []action.Action {
			return []action.Action{fakeAction{id: "delete", label: "delete", result: action.Result{Status: "trashed", Refresh: true}}}
		},
		Reload: func() ([]session.Session, error) { return nil, errors.New("reading ~/.claude: permission denied") },
	})
	m = feed(m, tea.WindowSizeMsg{Width: 120, Height: 24}, ctrlP, tea.KeyMsg{Type: tea.KeyEnter})

	if len(m.visible()) != 2 {
		t.Errorf("visible() = %d rows, want the previous list kept when a reload yields nothing", len(m.visible()))
	}
	if m.loadErr == nil {
		t.Error("loadErr = nil, want the failed reload reported")
	}
}

func TestPaletteHalfPageKeysMoveWithinTheEntryRange(t *testing.T) {
	m := newModel(Config{
		Sessions: paletteRows(),
		Actions:  func(session.Session) []action.Action { return manyFakeActions(40) },
	})
	m = feed(m, tea.WindowSizeMsg{Width: 120, Height: 20}, ctrlP)
	half := m.paletteHalfPage()

	before := m.paletteCursor
	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlD})
	if m.paletteCursor <= before || m.paletteCursor > before+half {
		t.Errorf("paletteCursor = %d, want it advanced by at most a half page (%d) from %d", m.paletteCursor, half, before)
	}
	assertSelectedPaletteEntryRendered(t, m)

	m = feed(m, repeatMsg(20, tea.KeyMsg{Type: tea.KeyCtrlD})...)
	if got, want := m.paletteCursor, lastRunnable(m); got != want {
		t.Errorf("paletteCursor = %d, want %d; ctrl+d must not walk past the last entry", got, want)
	}
	assertSelectedPaletteEntryRendered(t, m)

	m = feed(m, repeatMsg(20, tea.KeyMsg{Type: tea.KeyCtrlU})...)
	if got, want := m.paletteCursor, firstRunnable(m); got != want {
		t.Errorf("paletteCursor = %d, want %d; ctrl+u must not walk above the first entry", got, want)
	}
	assertSelectedPaletteEntryRendered(t, m)
}

func TestPaletteHintNamesTheMovementKeys(t *testing.T) {
	m := feed(paletteModel(t, []action.Action{fakeAction{id: "a", label: "aaa"}}, nil), ctrlP)

	view := m.View()
	for _, want := range []string{"↑/↓", "^u/^d", "enter", "esc"} {
		if !strings.Contains(view, want) {
			t.Errorf("palette hint does not mention %q:\n%s", want, view)
		}
	}
}

// The palette's own furniture is given up cheapest-first, like the row list's:
// its title and the blank under it, then the blank above the footer, then the
// footer hint, then the filter line. One entry always survives, and a long
// session name in the title is cut to the terminal rather than wrapped.
func TestPaletteViewFitsEveryHeightAndWidth(t *testing.T) {
	queries := map[string]rune{"matching": 'a', "matching nothing": 'z'}
	for name, query := range queries {
		for _, height := range []int{1, 2, 3, 4, 5, 6, 10, 24} {
			for _, width := range []int{40, 80, 120} {
				t.Run(fmt.Sprintf("%s/height=%d/width=%d", name, height, width), func(t *testing.T) {
					m := newModel(Config{
						Sessions: []session.Session{{Agent: "claude", ID: "a", Name: "日本語のセッション名です 🚀 that runs on and on and on", Live: true, PID: 1, Status: "running", LastActive: time.Now()}},
						Actions:  func(session.Session) []action.Action { return manyFakeActions(40) },
					})
					m = feed(m, tea.WindowSizeMsg{Width: width, Height: height}, ctrlP, keyMsg(query))

					view := m.View()
					lines := strings.Split(view, "\n")
					if len(lines) > height {
						t.Errorf("paletteView() rendered %d lines, want at most %d:\n%s", len(lines), height, view)
					}
					for _, line := range lines {
						if cells := lipgloss.Width(line); cells > width {
							t.Errorf("palette line is %d cells wide, wider than the %d-column terminal: %q", cells, width, line)
						}
					}
					if query == 'a' && len(paletteEntryLines(view)) == 0 {
						t.Errorf("paletteView() rendered no entry at all:\n%s", view)
					}
					if query == 'z' && !strings.Contains(view, "no matching action") {
						t.Errorf("paletteView() dropped the empty-state line it reserved room for:\n%s", view)
					}
				})
			}
		}
	}
}

func groupedActions() []action.Action {
	return []action.Action{
		fakeAction{id: "ticket.open", label: "open in Linear - Ticket ENG-3130", group: action.GroupGoTo},
		fakeAction{id: "copy.resume", label: "copy resume command", group: action.GroupClipboard},
		fakeAction{id: "copy.id", label: "copy session id", group: action.GroupClipboard},
		fakeAction{id: "launch.vcs", label: "open in Fork", group: action.GroupGoTo},
		fakeAction{id: "process.kill", label: "kill process (pid 1)", group: action.GroupDanger},
	}
}

func groupedPalette(t *testing.T) model {
	t.Helper()
	m := newModel(Config{
		Sessions: paletteRows(),
		Actions:  func(session.Session) []action.Action { return groupedActions() },
		Commands: func() []Command { return []Command{{Label: "reload sessions"}} },
	})
	return feed(m, tea.WindowSizeMsg{Width: 100, Height: 24}, ctrlP)
}

// Ten flat entries read as a wall. The groups are drawn in the order the Group
// constants declare, not the order the registry happened to produce, so the same
// action is always in the same place.
func TestPaletteGroupsEntriesUnderHeadersInGroupOrder(t *testing.T) {
	m := groupedPalette(t)

	var headers []string
	for _, e := range m.paletteItems {
		if e.header {
			headers = append(headers, e.label)
		}
	}
	want := []string{"go to", "clipboard", "danger", "picker"}
	if strings.Join(headers, ",") != strings.Join(want, ",") {
		t.Errorf("headers = %v, want %v", headers, want)
	}

	// Every runnable entry sits under its own group's header.
	current := ""
	for _, e := range m.paletteItems {
		switch {
		case e.header:
			current = e.label
		case e.runnable() && current != e.group.String():
			t.Errorf("%q is drawn under %q, want it under %q", e.label, current, e.group.String())
		}
	}
}

// Within a group the order is the registry's, so an action never moves because
// someone reworded its label.
func TestPaletteKeepsRegistryOrderWithinAGroup(t *testing.T) {
	m := groupedPalette(t)

	got := paletteLabels(m)
	want := []string{
		"open in Linear - Ticket ENG-3130", "open in Fork",
		"copy resume command", "copy session id",
		"kill process (pid 1)",
		"reload sessions",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("entries = %v, want %v", got, want)
	}
}

// A command acts on the picker, not on the session the palette was opened for,
// which is why it is not filed alongside that session's own actions.
func TestPaletteFilesCommandsUnderPicker(t *testing.T) {
	m := groupedPalette(t)

	for _, e := range m.paletteItems {
		if e.command != nil && e.group != action.GroupPicker {
			t.Errorf("command %q is in group %v, want GroupPicker", e.label, e.group)
		}
	}
}

// The cursor must never rest on a header: enter would do nothing and the key
// would look broken.
func TestPaletteCursorNeverRestsOnAHeader(t *testing.T) {
	m := groupedPalette(t)

	if e := m.paletteItems[m.paletteCursor]; !e.runnable() {
		t.Fatalf("the palette opened with the cursor on %q, which cannot be run", e.label)
	}

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyDown}, {Type: tea.KeyUp},
		{Type: tea.KeyCtrlD}, {Type: tea.KeyCtrlU},
	} {
		walk := m
		for step := range len(walk.paletteItems) + 4 {
			next, _ := walk.Update(key)
			walk = next.(model)
			if e := walk.paletteItems[walk.paletteCursor]; !e.runnable() {
				t.Fatalf("%v step %d left the cursor on %q (header=%v blank=%v)", key.Type, step, e.label, e.header, e.blank)
			}
		}
	}
}

// Moving down out of a group has to clear both the separator and the next
// header in one keypress, or the key reads as doing nothing twice.
func TestPaletteDownCrossesAGroupBoundaryInOneKeypress(t *testing.T) {
	m := groupedPalette(t)

	// Walk to the last entry of the first group.
	for m.paletteItems[m.paletteCursor].label != "open in Fork" {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		if next.(model).paletteCursor == m.paletteCursor {
			t.Fatal("setup: never reached the end of the first group")
		}
		m = next.(model)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := next.(model).paletteItems[next.(model).paletteCursor].label; got != "copy resume command" {
		t.Errorf("down landed on %q, want the first entry of the next group", got)
	}
}

// A heading over nothing reads as an entry that failed to draw.
func TestPaletteFilterDropsHeadersOfEmptiedGroups(t *testing.T) {
	m := feed(groupedPalette(t), runes("copy")...)

	var headers []string
	for _, e := range m.paletteItems {
		if e.header {
			headers = append(headers, e.label)
		}
	}
	if strings.Join(headers, ",") != "clipboard" {
		t.Errorf("headers = %v, want only clipboard", headers)
	}
	if got := paletteLabels(m); len(got) != 2 {
		t.Errorf("entries = %v, want the two copy actions", got)
	}
	if view := m.View(); strings.Contains(view, "danger") || strings.Contains(view, "go to") {
		t.Errorf("View() kept a header whose group the filter emptied:\n%s", view)
	}
}

// A filter matching nothing must leave no headers standing either.
func TestPaletteFilterMatchingNothingDrawsNoHeaders(t *testing.T) {
	m := feed(groupedPalette(t), runes("zzzz")...)

	if len(m.paletteItems) != 0 {
		t.Errorf("paletteItems = %+v, want nothing", m.paletteItems)
	}
	view := m.View()
	if !strings.Contains(view, "no matching action") {
		t.Errorf("View() does not say nothing matched:\n%s", view)
	}
	for _, header := range []string{"go to", "clipboard", "danger", "picker"} {
		if strings.Contains(view, header) {
			t.Errorf("View() still draws the %q header with nothing under it:\n%s", header, view)
		}
	}
}

// Enter on a header would be a keypress that silently does nothing. The cursor
// is kept off headers, so this is the belt to that braces.
func TestPaletteEnterOnAHeaderRunsNothing(t *testing.T) {
	var ran []string
	m := newModel(Config{
		Sessions: paletteRows(),
		Actions: func(session.Session) []action.Action {
			return []action.Action{fakeAction{id: "a", label: "an action", ran: &ran}}
		},
	})
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 24}, ctrlP)

	m.paletteCursor = 0
	if m.paletteItems[0].runnable() {
		t.Fatal("setup: entry 0 is not a header")
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("enter on a header dispatched a Cmd")
	}
	if len(ran) != 0 {
		t.Errorf("ran = %v, want nothing run", ran)
	}
	if !next.(model).palette {
		t.Error("enter on a header closed the palette")
	}
}
