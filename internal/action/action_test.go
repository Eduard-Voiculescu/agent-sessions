package action

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

type stubAction struct {
	id        string
	available bool
}

func (s stubAction) ID() string                     { return s.id }
func (s stubAction) Group() Group                   { return GroupGoTo }
func (s stubAction) Label(session.Session) string   { return s.id }
func (s stubAction) Available(session.Session) bool { return s.available }
func (s stubAction) Confirm(session.Session) string { return "" }
func (s stubAction) Run(context.Context, session.Session) (Result, error) {
	return Result{Status: s.id + " ran"}, nil
}

func ids(actions []Action) []string {
	out := make([]string, 0, len(actions))
	for _, a := range actions {
		out = append(out, a.ID())
	}
	return out
}

func TestRegistryForIncludesContributedActions(t *testing.T) {
	reg := NewRegistry(Config{
		Contribute: func(agent string) []Action {
			if agent != "claude" {
				return nil
			}
			return []Action{stubAction{id: "claude.delete", available: true}}
		},
	})

	got := ids(reg.For(session.Session{Agent: "claude", ID: "a", Cwd: "/repo"}))

	var found bool
	for _, id := range got {
		if id == "claude.delete" {
			found = true
		}
	}
	if !found {
		t.Errorf("For() = %v, want it to include claude.delete", got)
	}
}

func TestRegistryForOmitsUnavailableActions(t *testing.T) {
	reg := NewRegistry(Config{
		Contribute: func(string) []Action {
			return []Action{
				stubAction{id: "yes", available: true},
				stubAction{id: "no", available: false},
			}
		},
	})

	for _, id := range ids(reg.For(session.Session{Agent: "claude"})) {
		if id == "no" {
			t.Error("For() included an unavailable action")
		}
	}
}

func TestRegistryForWorksWithoutAnyContributor(t *testing.T) {
	reg := NewRegistry(Config{})

	if len(reg.For(session.Session{Agent: "claude", ID: "a", Cwd: "/repo"})) == 0 {
		t.Error("For() returned nothing; built-in actions should still be present")
	}
}

func contains(ids []string, id string) bool {
	for _, got := range ids {
		if got == id {
			return true
		}
	}
	return false
}

func TestForDoesNotLeakContributedActionsAcrossAgents(t *testing.T) {
	// builtins must have spare capacity so that appending to it can write into
	// the shared backing array instead of allocating a fresh one: this is the
	// only shape under which a single, unsplit append could leak one agent's
	// contributed action into another's. For() always copies its filtered
	// result into a freshly allocated slice before returning, so two
	// *sequential* calls can never observe the corruption through their
	// return values — only two concurrent calls racing to append into the
	// same backing array can, which is why this test calls For() from two
	// goroutines and relies on the race detector to catch the shared write.
	spareCapacityBuiltins := make([]Action, 0, 8)
	spareCapacityBuiltins = append(spareCapacityBuiltins,
		stubAction{id: "builtin.one", available: true},
		stubAction{id: "builtin.two", available: true},
	)
	reg := &Registry{
		builtins: spareCapacityBuiltins,
		contribute: func(agent string) []Action {
			return []Action{stubAction{id: agent + ".only", available: true}}
		},
	}

	// A single pair of goroutines only sometimes overlaps enough for the race
	// detector to observe the shared write, so this repeats the race with a
	// start barrier that forces both goroutines to append at the same instant
	// on every trial.
	for trial := 0; trial < 100; trial++ {
		var claudeActions, codexActions []Action
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			claudeActions = reg.For(session.Session{Agent: "claude"})
		}()
		go func() {
			defer wg.Done()
			<-start
			codexActions = reg.For(session.Session{Agent: "codex"})
		}()
		close(start)
		wg.Wait()

		claudeIDs := ids(claudeActions)
		codexIDs := ids(codexActions)

		if !contains(claudeIDs, "claude.only") {
			t.Fatalf("trial %d: For(claude) = %v, want it to include claude.only", trial, claudeIDs)
		}
		if contains(claudeIDs, "codex.only") {
			t.Fatalf("trial %d: For(claude) = %v, want it to exclude codex.only", trial, claudeIDs)
		}
		if !contains(codexIDs, "codex.only") {
			t.Fatalf("trial %d: For(codex) = %v, want it to include codex.only", trial, codexIDs)
		}
		if contains(codexIDs, "claude.only") {
			t.Fatalf("trial %d: For(codex) = %v, want it to exclude claude.only", trial, codexIDs)
		}
	}
}

func TestRegistryHidesTheIDsTheOwnerAskedToHide(t *testing.T) {
	reg := NewRegistry(Config{
		Hidden:    []string{"copy.transcript", "process.kill"},
		Clipboard: func(string) error { return nil },
		Alive:     func(int) bool { return true },
		Signal:    func(int, int) error { return nil },
	})

	s := session.Session{Agent: "claude", ID: "abc", Transcript: "/t/a.jsonl", Live: true, PID: 7}
	for _, a := range reg.For(s) {
		if a.ID() == "copy.transcript" || a.ID() == "process.kill" {
			t.Errorf("%s is still offered after being hidden", a.ID())
		}
	}
	if !available(reg, s, "copy.id") {
		t.Error("hiding two actions removed a third")
	}
}

// Hiding must reach provider-contributed actions too, or the one destructive
// action that is not a built-in cannot be turned off.
func TestRegistryHidesAContributedAction(t *testing.T) {
	reg := NewRegistry(Config{
		Hidden:     []string{"session.delete"},
		Contribute: func(string) []Action { return []Action{stubAction{id: "session.delete", available: true}} },
	})

	for _, a := range reg.For(session.Session{Agent: "claude"}) {
		if a.ID() == "session.delete" {
			t.Error("a contributed action survived being hidden")
		}
	}
}

// An unavailable action still appears with its reason, because there is
// something to correct. A hidden one is gone.
func TestHidingIsNotTheSameAsUnavailable(t *testing.T) {
	s := session.Session{Agent: "claude", GitBranch: "eng-3130-x"}

	shown := NewRegistry(Config{Prefixes: []string{"eng"}})
	if !available(shown, s, "ticket.open") {
		t.Fatal("setup: an unavailable ticket action should still be offered")
	}

	hidden := NewRegistry(Config{Prefixes: []string{"eng"}, Hidden: []string{"ticket.open"}})
	if available(hidden, s, "ticket.open") {
		t.Error("a hidden action is still offered")
	}
}

// Hiding everything must leave the palette an empty list to draw its own empty
// state from, not a broken frame.
func TestHidingEverythingLeavesNoActions(t *testing.T) {
	reg := NewRegistry(Config{
		Clipboard:  func(string) error { return nil },
		Alive:      func(int) bool { return true },
		Signal:     func(int, int) error { return nil },
		LookPath:   func(tool string) (string, error) { return "/usr/local/bin/" + tool, nil },
		Contribute: func(string) []Action { return []Action{stubAction{id: "session.delete", available: true}} },
	})
	all := reg.IDs([]string{"claude"})

	hidden := NewRegistry(Config{
		Hidden:     all,
		Clipboard:  func(string) error { return nil },
		Alive:      func(int) bool { return true },
		Signal:     func(int, int) error { return nil },
		LookPath:   func(tool string) (string, error) { return "/usr/local/bin/" + tool, nil },
		Contribute: func(string) []Action { return []Action{stubAction{id: "session.delete", available: true}} },
	})

	s := session.Session{Agent: "claude", ID: "abc", Cwd: "/x", Transcript: "/t/a.jsonl", GitBranch: "eng-1-x", Live: true, PID: 7}
	if got := hidden.For(s); len(got) != 0 {
		t.Errorf("For() = %v, want nothing when every id is hidden", got)
	}
}

// The hide list is validated against IDs, so a list that omitted an action
// would let a typo through as if it were valid.
func TestRegistryIDsCoversBuiltInsAndContributions(t *testing.T) {
	reg := NewRegistry(Config{
		LookPath:   func(tool string) (string, error) { return "/usr/local/bin/" + tool, nil },
		Contribute: func(string) []Action { return []Action{stubAction{id: "session.delete", available: true}} },
	})

	ids := reg.IDs([]string{"claude"})
	for _, want := range []string{
		"ticket.open", "copy.resume", "copy.id", "copy.transcript", "copy.cwd",
		"launch.editor", "launch.vcs", "pr.open", "process.kill", "session.delete",
	} {
		if !slices.Contains(ids, want) {
			t.Errorf("IDs() = %v, missing %q", ids, want)
		}
	}
	if !slices.IsSorted(ids) {
		t.Errorf("IDs() = %v, want it sorted so an error message is stable between runs", ids)
	}
}
