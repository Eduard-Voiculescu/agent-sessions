package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

// startSessions holds two directories, one of them twice, so the ranking has
// something to rank.
func startSessions() []session.Session {
	now := time.Now()
	return []session.Session{
		{Agent: "claude", ID: "a", Name: "api", Cwd: "/Users/dev/git/api", LastActive: now},
		{Agent: "claude", ID: "b", Name: "api again", Cwd: "/Users/dev/git/api", LastActive: now},
		{Agent: "opencode", ID: "c", Name: "web", Cwd: "/Users/dev/git/web", LastActive: now},
	}
}

func startConfig(start func(context.Context, string, string) error) Config {
	return Config{
		Sessions:    startSessions(),
		StartAgents: []string{"claude", "opencode"},
		Start:       start,
	}
}

func openStart(t *testing.T, cfg Config) model {
	t.Helper()

	next, _ := newModel(cfg).Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	m := next.(model)
	if !m.starting {
		t.Fatalf("model.starting = false after ctrl+n, status %q", m.status)
	}
	return m
}

func TestStartCtrlNOpensTheAgentStep(t *testing.T) {
	m := openStart(t, startConfig(nil))

	if m.startStep != startStepAgent {
		t.Errorf("startStep = %v, want the agent step", m.startStep)
	}
	if got := m.startAgents; len(got) != 2 || got[0] != "claude" {
		t.Errorf("startAgents = %v, want the configured agents", got)
	}
}

func TestStartCtrlNSaysWhyWhenNoAgentCanStart(t *testing.T) {
	cfg := startConfig(nil)
	cfg.StartAgents = nil

	next, _ := newModel(cfg).Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	m := next.(model)

	if m.starting {
		t.Error("model.starting = true, want the mode refused with no agent to start")
	}
	if m.status == "" {
		t.Error("status is empty, want it to say no agent can be started")
	}
}

// ctrl+n is intercepted before the mode dispatch, like ctrl+p, but it must not
// reach past a mode that owns the keyboard: the palette's own filter and the
// compose box both take arbitrary keys.
func TestStartCtrlNIsIgnoredWhileAnotherModeOwnsTheKeyboard(t *testing.T) {
	tests := []struct {
		name string
		open func(model) model
	}{
		{name: "palette", open: func(m model) model { return m.openPalette() }},
		{name: "preview", open: func(m model) model { m.previewing = true; return m }},
		{name: "compose", open: func(m model) model { m.previewing = true; m.composing = true; return m }},
		{name: "confirm", open: func(m model) model { m.confirming = true; return m }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.open(newModel(startConfig(nil)))

			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})

			if next.(model).starting {
				t.Errorf("model.starting = true, want ctrl+n left to the %s", tt.name)
			}
		})
	}
}

func TestStartEnterOnAnAgentAdvancesToTheDirectoryStep(t *testing.T) {
	m := openStart(t, startConfig(nil))

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	next, _ = next.(model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if m.startStep != startStepDir {
		t.Fatalf("startStep = %v, want the directory step", m.startStep)
	}
	if m.startAgent != "opencode" {
		t.Errorf("startAgent = %q, want the highlighted agent", m.startAgent)
	}
}

func TestStartRanksDirectoriesByHowManySessionsTheyHold(t *testing.T) {
	m := openStart(t, startConfig(nil))
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if len(m.startDirs) != 2 {
		t.Fatalf("startDirs = %v, want one entry per distinct directory", m.startDirs)
	}
	if m.startDirs[0].path != "/Users/dev/git/api" || m.startDirs[0].sessions != 2 {
		t.Errorf("startDirs[0] = %+v, want the two-session directory first", m.startDirs[0])
	}
}

func TestStartTypingFiltersTheDirectories(t *testing.T) {
	m := openStart(t, startConfig(nil))
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	next, _ = next.(model).Update(keyMsg('w'))
	m = next.(model)

	if got := m.startCandidates(); len(got) != 1 || got[0].path != "/Users/dev/git/web" {
		t.Errorf("candidates = %+v, want only the directory matching \"w\"", got)
	}
}

// A directory with no session yet is reachable only by typing it, so a typed
// path is offered as itself rather than filtered against a list it is not on.
func TestStartOffersATypedPathVerbatim(t *testing.T) {
	m := openStart(t, startConfig(nil))
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	for _, r := range "/tmp/fresh" {
		next, _ = m.Update(keyMsg(r))
		m = next.(model)
	}

	got := m.startCandidates()
	if len(got) == 0 || got[0].path != "/tmp/fresh" {
		t.Fatalf("candidates = %+v, want the typed path offered first", got)
	}
	if !got[0].typed {
		t.Error("the typed entry is not marked as typed, so the view cannot say so")
	}
}

func TestStartEnterStartsTheChosenAgentInTheChosenDirectory(t *testing.T) {
	var gotAgent, gotDir string
	m := openStart(t, startConfig(func(_ context.Context, agent, dir string) error {
		gotAgent, gotDir = agent, dir
		return nil
	}))

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, cmd := next.(model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if cmd == nil {
		t.Fatal("Update() returned no Cmd, want the spawn handed to one")
	}
	if m.starting {
		t.Error("model.starting = true, want the mode closed once the start is under way")
	}
	msg := cmd()
	if gotAgent != "claude" || gotDir != "/Users/dev/git/api" {
		t.Errorf("Start(%q, %q), want (\"claude\", \"/Users/dev/git/api\")", gotAgent, gotDir)
	}

	next, _ = m.Update(msg)
	if status := next.(model).status; !strings.Contains(status, "claude") || !strings.Contains(status, "api") {
		t.Errorf("status = %q, want it to name the agent and the directory", status)
	}
}

// The spawn shells out to osascript, which blocks on the Automation consent
// dialog the first time, so its failure arrives as a message rather than
// inline. A directory that has since been removed is fixable, so the mode
// reopens holding what was typed instead of making it all be retyped.
func TestStartAFailedStartReportsAndReopensTheDirectoryStep(t *testing.T) {
	m := openStart(t, startConfig(func(context.Context, string, string) error {
		return errors.New("cannot start in /Users/dev/git/api: no such file or directory")
	}))

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, cmd := next.(model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(cmd())
	m = next.(model)

	if !strings.Contains(m.status, "no such file") {
		t.Errorf("status = %q, want the failure reported", m.status)
	}
	if !m.starting || m.startStep != startStepDir {
		t.Errorf("starting = %v step = %v, want the directory step reopened", m.starting, m.startStep)
	}
}

func TestStartEnterWithNoStartWiredSaysSo(t *testing.T) {
	m := openStart(t, startConfig(nil))

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, cmd := next.(model).Update(tea.KeyMsg{Type: tea.KeyEnter})

	if cmd != nil {
		t.Error("Update() returned a Cmd, want nothing spawned with no Start wired")
	}
	if status := next.(model).status; status == "" {
		t.Error("status is empty, want it to say start is unavailable")
	}
}

func TestStartEscStepsBackThenCloses(t *testing.T) {
	m := openStart(t, startConfig(nil))
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	next, _ = next.(model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	back := next.(model)
	if !back.starting || back.startStep != startStepAgent {
		t.Fatalf("starting = %v step = %v, want esc back on the agent step", back.starting, back.startStep)
	}

	next, _ = back.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if closed := next.(model); closed.starting {
		t.Error("model.starting = true, want a second esc to close the mode")
	}
}

func TestStartBackspaceEditsTheQuery(t *testing.T) {
	m := openStart(t, startConfig(nil))
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, _ = next.(model).Update(keyMsg('w'))
	next, _ = next.(model).Update(tea.KeyMsg{Type: tea.KeyBackspace})

	if got := next.(model).startQuery; got != "" {
		t.Errorf("startQuery = %q, want backspace to have cleared it", got)
	}
}

// The candidates are frozen when the step opens for the same reason the palette
// freezes its entries: a tick re-sorts the sessions, and a directory that slid
// under the cursor would be started instead of the one that was aimed at.
func TestStartATickDoesNotReorderTheCandidates(t *testing.T) {
	cfg := startConfig(nil)
	cfg.Refresh = func() []session.Session {
		return []session.Session{{Agent: "claude", ID: "z", Cwd: "/Users/dev/git/zebra", Live: true, PID: 9}}
	}
	m := openStart(t, cfg)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	next, _ = next.(model).Update(tickMsg{})
	m = next.(model)

	if m.startDirs[0].path != "/Users/dev/git/api" {
		t.Errorf("startDirs[0] = %+v, want the snapshot the step opened with", m.startDirs[0])
	}
}

func TestStartViewNamesTheAgentsThenTheDirectories(t *testing.T) {
	m := openStart(t, startConfig(nil))
	m.width, m.height = 100, 24

	agents := m.View()
	if !strings.Contains(agents, "claude") || !strings.Contains(agents, "opencode") {
		t.Errorf("the agent step does not list the agents:\n%s", agents)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	dirs := next.(model).View()
	if !strings.Contains(dirs, "git/api") {
		t.Errorf("the directory step does not list the directories:\n%s", dirs)
	}
}

// The point of a modal over a full-screen chooser: the rows stay on screen, so
// what is already running is visible while a new one is being started.
func TestStartModalFloatsOverTheSessionList(t *testing.T) {
	m := openStart(t, startConfig(nil))
	m.width, m.height = 100, 24

	view := m.View()
	lines := strings.Split(view, "\n")

	if len(lines) != 24 {
		t.Errorf("View() rendered %d lines, want the terminal's own 24", len(lines))
	}
	// AGENT and AGE sit either side of the box: the columns between them are
	// behind it, which is what a modal is.
	for _, want := range []string{"AGENT", "AGE", "╭─ start a session", "esc ─╮"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() is missing %q:\n%s", want, view)
		}
	}
	for _, line := range lines {
		if cells := lipgloss.Width(line); cells > 100 {
			t.Errorf("line is %d cells wide, wider than the 100-column terminal: %q", cells, line)
		}
	}
}

func TestStartModalFallsBackToTheFullScreenChooserWhenABoxWillNotFit(t *testing.T) {
	m := openStart(t, startConfig(nil))
	m.width, m.height = 30, 24

	view := m.View()

	if strings.Contains(view, "╭─ start") {
		t.Errorf("a box was drawn on a terminal with no room for one:\n%s", view)
	}
	if !strings.Contains(view, "claude") {
		t.Errorf("the fallback chooser lists no agent:\n%s", view)
	}
}

func TestStartModalShowsTheDirectoriesAndTheirCounts(t *testing.T) {
	m := openStart(t, startConfig(nil))
	m.width, m.height = 100, 24
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	view := next.(model).View()

	for _, want := range []string{"git/api", "2 sessions", "search"} {
		if !strings.Contains(view, want) {
			t.Errorf("the directory modal is missing %q:\n%s", want, view)
		}
	}
}
