package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eduardvoiculescu/agent-sessions/internal/action"
	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

func renameSessions() []session.Session {
	now := time.Now()
	return []session.Session{
		{Agent: "claude", ID: "live", Name: "webapp-8e", Cwd: "/Users/dev/git/webapp", Live: true, PID: 7, LastActive: now},
		{Agent: "claude", ID: "hist", Name: "Fix search ranking", Cwd: "/Users/dev/git/billing", LastActive: now.Add(-time.Hour)},
	}
}

// renameRecorder stands in for the store: what was asked for, and whether it was
// asked at all.
type renameRecorder struct {
	target session.Session
	name   string
	calls  int
	err    error
}

func (r *renameRecorder) rename(s session.Session, name string) error {
	r.calls++
	r.target, r.name = s, name
	return r.err
}

func renameModel(t *testing.T, recorder *renameRecorder) model {
	t.Helper()

	m := newModel(Config{
		Sessions: renameSessions(),
		Rename:   recorder.rename,
		Reload:   func() ([]session.Session, error) { return renameSessions(), nil },
	})
	return feed(m, tea.WindowSizeMsg{Width: 100, Height: 24})
}

func TestRenameOpensOnTheHighlightedRowPrefilledWithItsName(t *testing.T) {
	m := renameModel(t, &renameRecorder{})

	m = feed(m, keyMsg('r'))

	if !m.renaming {
		t.Fatal("model.renaming = false after r")
	}
	if m.renameTarget.ID != "live" {
		t.Errorf("renameTarget.ID = %q, want the highlighted row", m.renameTarget.ID)
	}
	// Prefilled rather than empty: a rename is usually an edit of what is there,
	// and retyping a name to change one word is the wrong price.
	if m.renameText != "webapp-8e" {
		t.Errorf("renameText = %q, want the row's current name", m.renameText)
	}
}

func TestRenameCommitsWhatWasTyped(t *testing.T) {
	recorder := &renameRecorder{}
	m := feed(renameModel(t, recorder), keyMsg('r'))

	m = feed(typeInto(m, " overlay"), tea.KeyMsg{Type: tea.KeyEnter})

	if recorder.calls != 1 {
		t.Fatalf("rename called %d times, want 1", recorder.calls)
	}
	if recorder.name != "webapp-8e overlay" {
		t.Errorf("name = %q, want the edited name", recorder.name)
	}
	if recorder.target.ID != "live" {
		t.Errorf("target.ID = %q, want the row it was opened on", recorder.target.ID)
	}
	if m.renaming {
		t.Error("model.renaming = true after enter, want the prompt closed")
	}
}

// The rename is aimed at a captured session, not at whatever the cursor points
// at when enter arrives: a tick re-sorts the rows while somebody is typing.
func TestRenameSurvivesATickReorderingTheRows(t *testing.T) {
	recorder := &renameRecorder{}
	m := feed(renameModel(t, recorder), keyMsg('r'))

	m = feed(m, tickMsg{})
	m.cursor = 1
	m = feed(m, tea.KeyMsg{Type: tea.KeyEnter})

	if recorder.target.ID != "live" {
		t.Errorf("target.ID = %q, want the session the prompt was opened on", recorder.target.ID)
	}
}

func TestRenameEscLeavesTheNameAlone(t *testing.T) {
	recorder := &renameRecorder{}
	m := feed(renameModel(t, recorder), keyMsg('r'))

	m = feed(typeInto(m, "!!"), tea.KeyMsg{Type: tea.KeyEsc})

	if recorder.calls != 0 {
		t.Error("esc renamed it anyway")
	}
	if m.renaming {
		t.Error("model.renaming = true after esc")
	}
}

// An emptied prompt is how a custom name is taken off: the row falls back to
// whatever the agent calls it, which is the same thing the palette's clear entry
// does.
func TestRenameEmptiedClearsTheCustomName(t *testing.T) {
	recorder := &renameRecorder{}
	m := feed(renameModel(t, recorder), keyMsg('r'))

	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlU}, tea.KeyMsg{Type: tea.KeyEnter})

	if recorder.calls != 1 {
		t.Fatalf("rename called %d times, want 1", recorder.calls)
	}
	if recorder.name != "" {
		t.Errorf("name = %q, want empty to mean clear", recorder.name)
	}
}

func TestRenameReportsAStoreThatRefused(t *testing.T) {
	recorder := &renameRecorder{err: errors.New("a name cannot contain a control character")}
	m := feed(renameModel(t, recorder), keyMsg('r'))

	m = feed(m, tea.KeyMsg{Type: tea.KeyEnter})

	if !strings.Contains(m.status, "control character") {
		t.Errorf("status = %q, want the refusal reported", m.status)
	}
	// The prompt stays open holding the text: the name is fixable, and retyping it
	// is the wrong price for a rejected character.
	if !m.renaming {
		t.Error("model.renaming = false, want the prompt kept open on a refusal")
	}
}

func TestRenameBackspaceEdits(t *testing.T) {
	m := feed(renameModel(t, &renameRecorder{}), keyMsg('r'))

	m = feed(m, tea.KeyMsg{Type: tea.KeyBackspace})

	if m.renameText != "webapp-8" {
		t.Errorf("renameText = %q, want a character removed", m.renameText)
	}
}

func TestRenameKeyDoesNothingWithNoStoreWired(t *testing.T) {
	m := feed(newModel(Config{Sessions: renameSessions()}), tea.WindowSizeMsg{Width: 100, Height: 24}, keyMsg('r'))

	if m.renaming {
		t.Error("model.renaming = true with no Rename wired")
	}
	if m.status == "" {
		t.Error("status is empty, want it to say renaming is unavailable")
	}
}

// The palette is the other way in, and the one the feature was asked for.
func TestPaletteOffersRenameInItsSessionGroup(t *testing.T) {
	m := feed(renameModel(t, &renameRecorder{}), ctrlP)

	var found bool
	for _, e := range m.paletteItems {
		if strings.Contains(e.label, "rename") {
			found = true
			if e.group != action.GroupSession {
				t.Errorf("rename is in group %v, want GroupSession", e.group)
			}
			if !e.runnable() {
				t.Error("the rename entry is not runnable")
			}
		}
	}
	if !found {
		t.Fatalf("the palette offers no rename entry: %v", paletteLabels(m))
	}
}

func TestPaletteRenameEntryOpensThePrompt(t *testing.T) {
	m := feed(renameModel(t, &renameRecorder{}), ctrlP)

	for i, e := range m.paletteItems {
		if strings.Contains(e.label, "rename") {
			m.paletteCursor = i
		}
	}
	m = feed(m, tea.KeyMsg{Type: tea.KeyEnter})

	if !m.renaming {
		t.Fatal("model.renaming = false, want the palette entry to open the prompt")
	}
	if m.palette {
		t.Error("model.palette = true, want the palette closed behind the prompt")
	}
	if m.renameTarget.ID != "live" {
		t.Errorf("renameTarget.ID = %q, want the session the palette was opened on", m.renameTarget.ID)
	}
}

// Clearing is only worth offering against a session that has a custom name.
func TestPaletteOffersClearingOnlyForARenamedSession(t *testing.T) {
	unnamed := feed(renameModel(t, &renameRecorder{}), ctrlP)
	for _, e := range unnamed.paletteItems {
		if strings.Contains(e.label, "clear") {
			t.Errorf("the palette offers clearing for a session with no custom name: %q", e.label)
		}
	}

	named := renameSessions()
	named[0].Label = "mine"
	m := newModel(Config{Sessions: named, Rename: (&renameRecorder{}).rename})
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 24}, ctrlP)

	var found bool
	for _, e := range m.paletteItems {
		if strings.Contains(e.label, "clear") {
			found = true
		}
	}
	if !found {
		t.Errorf("the palette offers no clear entry for a renamed session: %v", paletteLabels(m))
	}
}

func TestRenamePromptIsDrawnWithWhatIsTyped(t *testing.T) {
	m := feed(renameModel(t, &renameRecorder{}), keyMsg('r'))

	view := m.View()
	if !strings.Contains(view, "rename") || !strings.Contains(view, "webapp-8e") {
		t.Errorf("the prompt is not on screen:\n%s", view)
	}
}

// The footer carries the rename hint wherever there is room for it, and gives it
// up before it gives up "quit".
func TestFooterHintsFitTheTerminalTheyAreDrawnOn(t *testing.T) {
	wide := hintsFor(120)
	if !strings.Contains(wide, "rename") {
		t.Errorf("a 120-column footer omits the rename hint: %q", wide)
	}

	narrow := hintsFor(80)
	for _, want := range []string{"preview", "actions", "jump", "live", "filter", "help", "quit"} {
		if !strings.Contains(narrow, want) {
			t.Errorf("an 80-column footer lost %q: %q", want, narrow)
		}
	}
}
