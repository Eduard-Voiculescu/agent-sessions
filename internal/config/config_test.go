package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFromReadsEverySection(t *testing.T) {
	path := write(t, `
# an editor
[editor]
default = vscode

[vcs]
default = fork

[tickets]
provider  = linear
workspace = acme
prefixes  = eng,  pay , ops

[forge]
provider = github

[actions]
hide = copy.transcript, process.kill
`)

	got, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if got.Editor != "vscode" || got.VCS != "fork" || got.Forge != "github" {
		t.Errorf("got %+v, want editor vscode, vcs fork, forge github", got)
	}
	if got.Tickets.Provider != "linear" || got.Tickets.Workspace != "acme" {
		t.Errorf("tickets = %+v", got.Tickets)
	}
	if strings.Join(got.Tickets.Prefixes, "|") != "eng|pay|ops" {
		t.Errorf("prefixes = %v, want each element trimmed", got.Tickets.Prefixes)
	}
	if strings.Join(got.Hidden, "|") != "copy.transcript|process.kill" {
		t.Errorf("hidden = %v", got.Hidden)
	}
	if got.Path != path {
		t.Errorf("Path = %q, want %q", got.Path, path)
	}
}

// A silently ignored typo is the single most common way a config file wastes an
// afternoon: the setting reads as applied and is not. Every message carries the
// line, because a config file is edited by hand.
func TestLoadFromRejectsWhatItCannotUnderstand(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{"unknown key", "[editor]\ndefualt = vscode\n", `:2: unknown key "defualt" in [editor]`},
		{"unknown section", "[editors]\ndefault = vscode\n", `:1: unknown section [editors]`},
		{"duplicate key", "[editor]\ndefault = vscode\ndefault = zed\n", `:3: duplicate key "default"`},
		{"key with no section", "default = vscode\n", `:1: key "default" outside any section`},
		{"not a pair", "[editor]\nvscode\n", `:2: expected "key = value"`},
		{"mixed case section", "[Editor]\ndefault = vscode\n", `:1: unknown section [Editor]`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFrom(write(t, tt.body))
			if err == nil {
				t.Fatalf("LoadFrom() error = nil, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("LoadFrom() error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

// The message has to name what is valid, or the owner is left guessing at the
// spelling they got wrong.
func TestLoadFromErrorsNameTheValidAlternatives(t *testing.T) {
	_, err := LoadFrom(write(t, "[editor]\ndefualt = vscode\n"))
	if err == nil || !strings.Contains(err.Error(), "known keys are default") {
		t.Errorf("error = %v, want it to list the known keys", err)
	}

	_, err = LoadFrom(write(t, "[editors]\n"))
	if err == nil {
		t.Fatal("error = nil for an unknown section")
	}
	for _, want := range []string{"actions", "editor", "forge", "tickets", "vcs"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to list section %q", err, want)
		}
	}
}

func TestLoadFromToleratesTheShapesARealFileTakes(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
	}{
		{"empty", ""},
		{"comments only", "# nothing here\n; nor here\n"},
		{"no trailing newline", "[editor]\ndefault = vscode"},
		{"crlf", "[editor]\r\ndefault = vscode\r\n"},
		{"blank lines and indentation", "\n\n  [editor]  \n   default   =   vscode   \n\n"},
		{"trailing comment", "[editor]\ndefault = vscode  # my editor\n"},
		{"value containing equals", "[tickets]\nworkspace = a=b\n"},
		{"empty value", "[editor]\ndefault =\n"},
		{"value that is only a comment", "[editor]\ndefault = # nothing yet\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := LoadFrom(write(t, tt.body)); err != nil {
				t.Errorf("LoadFrom() error = %v, want nil", err)
			}
		})
	}
}

// Trimming and comment-stripping are where a hand-rolled parser goes wrong, so
// the values are read back rather than only parsed without error.
func TestLoadFromReadsValuesBackExactly(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want File
	}{
		{"trailing comment stripped", "[editor]\ndefault = vscode  # my editor\n", File{Editor: "vscode"}},
		{"indentation trimmed", "  [editor]  \n   default   =   vscode   \n", File{Editor: "vscode"}},
		{"crlf trimmed", "[editor]\r\ndefault = vscode\r\n", File{Editor: "vscode"}},
		{"only the first equals separates", "[tickets]\nworkspace = a=b\n", File{Tickets: Tickets{Workspace: "a=b"}}},
		{"a value that is only a comment is empty", "[editor]\ndefault = # nothing yet\n", File{}},
		{"semicolon comment", "[editor]\ndefault = zed ; for now\n", File{Editor: "zed"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadFrom(write(t, tt.body))
			if err != nil {
				t.Fatalf("LoadFrom() error = %v", err)
			}
			if got.Editor != tt.want.Editor {
				t.Errorf("Editor = %q, want %q", got.Editor, tt.want.Editor)
			}
			if got.Tickets.Workspace != tt.want.Tickets.Workspace {
				t.Errorf("Workspace = %q, want %q", got.Tickets.Workspace, tt.want.Tickets.Workspace)
			}
		})
	}
}

// A machine with no config file is the common case, not a failure.
func TestLoadFromAMissingFileIsNotAnError(t *testing.T) {
	got, err := LoadFrom(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v, want nil", err)
	}
	if got.Path != "" {
		t.Errorf("Path = %q, want empty when no file was read", got.Path)
	}
	if got.Editor != "" || len(got.Hidden) != 0 {
		t.Errorf("got %+v, want a zero File", got)
	}
}

// A directory where a file was expected is a mistake worth reporting, not a
// missing file to shrug at.
func TestLoadFromADirectorySaysSo(t *testing.T) {
	if _, err := LoadFrom(t.TempDir()); err == nil {
		t.Error("LoadFrom() error = nil, want a directory reported")
	}
}

func TestPathPrefersTheExplicitOverrideThenXDGThenHome(t *testing.T) {
	t.Setenv("AGENT_SESSIONS_CONFIG", "/explicit/config")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := Path(); got != "/explicit/config" {
		t.Errorf("Path() = %q, want the explicit override to win", got)
	}

	t.Setenv("AGENT_SESSIONS_CONFIG", "")
	if got := Path(); got != filepath.Join("/xdg", "agent-sessions", "config") {
		t.Errorf("Path() = %q, want the XDG location", got)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got, want := Path(), filepath.Join(home, ".agent-sessions", "config"); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

// The file is named config, not .config: a dotfile inside a dot directory reads
// as a typo.
func TestPathDoesNotHideTheFileInsideAHiddenDirectory(t *testing.T) {
	t.Setenv("AGENT_SESSIONS_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if base := filepath.Base(Path()); base != "config" {
		t.Errorf("Path() basename = %q, want %q", base, "config")
	}
}

func TestLoadUsesTheResolvedPath(t *testing.T) {
	path := write(t, "[editor]\ndefault = zed\n")
	t.Setenv("AGENT_SESSIONS_CONFIG", path)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Editor != "zed" {
		t.Errorf("Editor = %q, want the file the override named", got.Editor)
	}
}

func TestLoadFromReadsTheSecondWaveSections(t *testing.T) {
	path := write(t, `
[list]
limit = 50

[filters]
live = true
cwd  = /srv/repos

[claude]
dir = /opt/claude
`)

	got, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if got.Limit == nil || *got.Limit != 50 {
		t.Errorf("Limit = %v, want 50", got.Limit)
	}
	if got.Live == nil || !*got.Live {
		t.Errorf("Live = %v, want true", got.Live)
	}
	if got.Cwd != "/srv/repos" || got.ClaudeDir != "/opt/claude" {
		t.Errorf("Cwd = %q, ClaudeDir = %q", got.Cwd, got.ClaudeDir)
	}
}

// A key set to its own zero value is a decision; an absent key is not. Reading
// both as zero would make "limit = 0" — meaning no limit — indistinguishable
// from saying nothing, and silently reinstate the default of 200.
func TestLoadFromTellsAZeroValueFromAnAbsentKey(t *testing.T) {
	set, err := LoadFrom(write(t, "[list]\nlimit = 0\n\n[filters]\nlive = false\n"))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if set.Limit == nil || *set.Limit != 0 {
		t.Errorf("Limit = %v, want a pointer to 0", set.Limit)
	}
	if set.Live == nil || *set.Live {
		t.Errorf("Live = %v, want a pointer to false", set.Live)
	}

	absent, err := LoadFrom(write(t, "[editor]\ndefault = zed\n"))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if absent.Limit != nil || absent.Live != nil {
		t.Errorf("Limit = %v, Live = %v; want both nil when unwritten", absent.Limit, absent.Live)
	}
}

// A typed value that is the wrong type must say where it was written.
func TestLoadFromReportsTypeErrorsWithTheirLine(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{"limit is not a number", "[list]\n\nlimit = plenty\n", `:3: limit in [list] must be a whole number, not "plenty"`},
		{"limit is negative", "[list]\nlimit = -5\n", `:2: limit in [list] cannot be negative`},
		{"limit is a float", "[list]\nlimit = 1.5\n", `:2: limit in [list] must be a whole number`},
		{"live is not a bool", "[filters]\n\n\nlive = maybe\n", `:4: live in [filters] must be true or false, not "maybe"`},
		// Guessing at synonyms is how one setting ends up with two spellings.
		{"live = yes is not accepted", "[filters]\nlive = yes\n", `must be true or false`},
		{"live = 1 is not accepted", "[filters]\nlive = 1\n", `must be true or false`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadFrom(write(t, tt.body))
			if err == nil {
				t.Fatalf("LoadFrom() error = nil, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("LoadFrom() error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

// No shell reads a config file, so a leading ~ arrives as a literal character.
// Left alone it makes a filter match nothing at all, silently — the worst way
// for a filter to be wrong.
func TestLoadFromExpandsALeadingTildeInPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	got, err := LoadFrom(write(t, "[filters]\ncwd = ~/git/acme\n\n[claude]\ndir = ~\n"))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if got.Cwd != filepath.Join(home, "git/acme") {
		t.Errorf("Cwd = %q, want the tilde expanded", got.Cwd)
	}
	if got.ClaudeDir != home {
		t.Errorf("ClaudeDir = %q, want %q", got.ClaudeDir, home)
	}
}

// Only a leading tilde before a separator. ~other is another user's home on
// some systems, and a path with a tilde in the middle is just a path.
func TestLoadFromLeavesOtherTildesAlone(t *testing.T) {
	got, err := LoadFrom(write(t, "[filters]\ncwd = /srv/~backup/repos\n"))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if got.Cwd != "/srv/~backup/repos" {
		t.Errorf("Cwd = %q, want it untouched", got.Cwd)
	}

	got, err = LoadFrom(write(t, "[filters]\ncwd = ~other/repos\n"))
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if got.Cwd != "~other/repos" {
		t.Errorf("Cwd = %q, want another user's home left unresolved", got.Cwd)
	}
}
