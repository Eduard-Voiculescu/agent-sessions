package label

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

func store(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "names.json"))
}

var key = session.Key{Agent: "claude", ID: "1f843491"}

func TestNamesOfAMachineThatHasRenamedNothing(t *testing.T) {
	names, err := store(t).Names()
	if err != nil {
		t.Fatalf("Names() error = %v, want nil — a missing file is the common case", err)
	}
	if len(names) != 0 {
		t.Errorf("Names() = %v, want empty", names)
	}
}

func TestSetThenNamesReadsItBack(t *testing.T) {
	s := store(t)

	if err := s.Set(key, "pet overlay work"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	names, err := s.Names()
	if err != nil {
		t.Fatalf("Names() error = %v", err)
	}
	if names[key] != "pet overlay work" {
		t.Errorf("Names()[%v] = %q, want the name that was set", key, names[key])
	}
}

// Two agents may mint the same id, so a name belongs to one agent's session and
// not to the id alone.
func TestNamesAreScopedToTheirAgent(t *testing.T) {
	s := store(t)
	other := session.Key{Agent: "opencode", ID: key.ID}

	if err := s.Set(key, "claude one"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(other, "opencode one"); err != nil {
		t.Fatal(err)
	}

	names, _ := s.Names()
	if names[key] != "claude one" || names[other] != "opencode one" {
		t.Errorf("Names() = %v, want both agents kept apart", names)
	}
}

func TestClearRemovesTheNameAndLeavesTheRest(t *testing.T) {
	s := store(t)
	other := session.Key{Agent: "claude", ID: "kept"}
	_ = s.Set(key, "going")
	_ = s.Set(other, "staying")

	if err := s.Clear(key); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}

	names, _ := s.Names()
	if _, found := names[key]; found {
		t.Error("the cleared name is still there")
	}
	if names[other] != "staying" {
		t.Errorf("Clear() took the other name too: %v", names)
	}
}

func TestClearingSomethingNeverNamedIsNotAnError(t *testing.T) {
	if err := store(t).Clear(key); err != nil {
		t.Errorf("Clear() error = %v, want nil", err)
	}
}

// A name is drawn on a terminal and written into a file this program owns, so
// what a terminal reads as a command is refused at the point it is typed rather
// than neutralised at each of the places it is later drawn.
func TestSetRefusesANameThatWouldNotDrawAsOne(t *testing.T) {
	tests := map[string]string{
		"empty":            "",
		"whitespace only":  "   ",
		"control sequence": "boom\x1b[2Jgone",
		"newline":          "two\nlines",
		"tab":              "a\tb",
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			s := store(t)
			if err := s.Set(key, value); err == nil {
				t.Errorf("Set(%q) error = nil, want a refusal", value)
			}
			names, _ := s.Names()
			if len(names) != 0 {
				t.Errorf("the refused name was stored anyway: %v", names)
			}
		})
	}
}

func TestSetRefusesANameTooLongForAnyColumn(t *testing.T) {
	if err := store(t).Set(key, strings.Repeat("x", MaxLength+1)); err == nil {
		t.Error("Set() error = nil, want a refusal")
	}
}

func TestSetTrimsTheEdges(t *testing.T) {
	s := store(t)
	_ = s.Set(key, "  spaced  ")

	names, _ := s.Names()
	if names[key] != "spaced" {
		t.Errorf("Names()[key] = %q, want it trimmed", names[key])
	}
}

// The file is somebody's own state, and half of a rewritten one is worse than
// none: a crash mid-write must leave the previous names readable.
func TestSetWritesThroughATemporaryFile(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "names.json"))
	if err := s.Set(key, "written"); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d files, want only names.json — a temporary was left behind", len(entries))
	}

	info, err := os.Stat(filepath.Join(dir, "names.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("names.json is %v, want 0600: it records what somebody called their work", mode)
	}
}

// A file somebody hand-edited into nonsense must not take the picker down with
// it: the names are a convenience, and the sessions are the point.
func TestNamesReportsAFileItCannotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := New(path).Names()
	if err == nil {
		t.Error("Names() error = nil, want the unreadable file reported")
	}
}

func TestApplyPutsNamesOntoTheSessionsThatHaveThem(t *testing.T) {
	sessions := []session.Session{
		{Agent: "claude", ID: key.ID, Name: "first prompt"},
		{Agent: "claude", ID: "untouched", Name: "other"},
	}

	Apply(sessions, map[session.Key]string{key: "mine"})

	if sessions[0].Label != "mine" || sessions[0].Name != "mine" {
		t.Errorf("session = %+v, want the label applied to both fields", sessions[0])
	}
	if sessions[1].Label != "" || sessions[1].Name != "other" {
		t.Errorf("an unnamed session was touched: %+v", sessions[1])
	}
}
