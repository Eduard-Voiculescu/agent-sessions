package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

func TestCellNarrowWidthsDoNotPanic(t *testing.T) {
	long := "this is a much longer value than the available column width"

	tests := []struct {
		name  string
		width int
		want  string
	}{
		{name: "zero width yields empty", width: 0, want: ""},
		{name: "width of one yields the ellipsis", width: 1, want: "…"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cell(long, tt.width); got != tt.want {
				t.Errorf("cell(%q, %d) = %q, want %q", long, tt.width, got, tt.want)
			}
		})
	}
}

func TestFilterOptions(t *testing.T) {
	now := time.Now()
	all := []session.Session{
		{Agent: "claude", ID: "live", Cwd: "/Users/dev/repo", Live: true, LastActive: now},
		{Agent: "claude", ID: "dead", Cwd: "/Users/dev/other", LastActive: now},
	}

	tests := []struct {
		name    string
		opts    Options
		wantIDs []string
	}{
		{name: "no filters", opts: Options{}, wantIDs: []string{"live", "dead"}},
		{name: "live only", opts: Options{LiveOnly: true}, wantIDs: []string{"live"}},
		{name: "cwd prefix", opts: Options{Cwd: "/Users/dev/other"}, wantIDs: []string{"dead"}},
		{name: "cwd prefix matching none", opts: Options{Cwd: "/nope"}, wantIDs: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterOptions(all, &tt.opts)
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("FilterOptions() returned %d sessions, want %d: %+v", len(got), len(tt.wantIDs), got)
			}
			for i, id := range tt.wantIDs {
				if got[i].ID != id {
					t.Errorf("FilterOptions()[%d].ID = %q, want %q", i, got[i].ID, id)
				}
			}
		})
	}
}

func TestRunListFlattensMultilineNamesIntoOneRow(t *testing.T) {
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "-tmp-repo")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	body := `{"type":"user","sessionId":"nlid","cwd":"/tmp/repo","gitBranch":"main","message":{"role":"user","content":"line one\nline two\nline three"}}` + "\n"
	if err := os.WriteFile(filepath.Join(projects, "nlid.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	opts := &Options{ClaudeDir: dir, Limit: 10}
	cmd := newListCommand(opts, runList)
	cmd.SetArgs([]string{})
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("output has %d lines, want 2 (header + one row): %q", len(lines), buf.String())
	}
}

// list and the picker must agree on what a row's status is, or the same session
// reads differently depending on which one you looked at.
func TestListStatusPrefersLiveThenTheJobRecord(t *testing.T) {
	for _, tt := range []struct {
		name string
		s    session.Session
		want string
	}{
		{"live with a status", session.Session{Live: true, Status: "busy"}, "busy"},
		{"live with no status", session.Session{Live: true}, "live"},
		{"live outranks a stale job record", session.Session{Live: true, Status: "busy", JobState: "done"}, "busy"},
		{"background agent", session.Session{JobState: "working"}, "working"},
		{"plain history", session.Session{}, "-"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := listStatus(tt.s); got != tt.want {
				t.Errorf("listStatus(%+v) = %q, want %q", tt.s, got, tt.want)
			}
		})
	}
}

// A fresh install with no config lists every agent the binary can read; the
// config file is for narrowing that, not for making it work.
func TestRegistryEnablesEveryProviderByDefault(t *testing.T) {
	all := registryFor(&Options{})
	if _, ok := all.Find("claude"); !ok {
		t.Error("claude is not registered with no config")
	}
	if _, ok := all.Find("opencode"); !ok {
		t.Error("opencode is not registered with no config")
	}
}

func TestRegistryHonoursTheEnableList(t *testing.T) {
	only := registryFor(&Options{Providers: []string{"claude"}})
	if _, ok := only.Find("claude"); !ok {
		t.Error("claude was excluded by an allowlist that names it")
	}
	if _, ok := only.Find("opencode"); ok {
		t.Error("opencode is registered despite an allowlist that omits it")
	}
}

// Every advertised provider name must construct, or the error message that
// greets a misspelling offers a value that does not work.
func TestEveryProviderNameConstructs(t *testing.T) {
	for _, name := range providerNames() {
		reg := registryFor(&Options{Providers: []string{name}})
		if _, ok := reg.Find(name); !ok {
			t.Errorf("providerNames() advertises %q but registryFor does not build it", name)
		}
	}
}

// --agent narrows to one agent. Without it a machine running two agents has no
// way to look at one of them.
func TestFilterOptionsNarrowsByAgent(t *testing.T) {
	sessions := []session.Session{
		{Agent: "claude", ID: "a", Cwd: "/w"},
		{Agent: "opencode", ID: "b", Cwd: "/w"},
	}

	got := FilterOptions(sessions, &Options{Agent: "opencode"})
	if len(got) != 1 || got[0].Agent != "opencode" {
		t.Errorf("FilterOptions() = %+v, want only the opencode session", got)
	}

	if len(FilterOptions(sessions, &Options{})) != 2 {
		t.Error("an unset --agent dropped sessions")
	}
	if len(FilterOptions(sessions, &Options{Agent: "gemini"})) != 0 {
		t.Error("an agent nobody has sessions from returned rows")
	}
}

// Two providers in one registry: both agents' sessions come back, and an id
// that collides across agents stays two rows. The merge key is (Agent, ID) for
// exactly this reason, and nothing but a second provider could exercise it.
func TestBothProvidersSessionsAppearAndCollidingIDsStayDistinct(t *testing.T) {
	claudeDir := t.TempDir()
	projects := filepath.Join(claudeDir, "projects", "-w-repo")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	// The same bare id under both agents.
	line := `{"type":"user","cwd":"/w/repo","gitBranch":"main","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(filepath.Join(projects, "shared.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	openDir := t.TempDir()
	sessions := filepath.Join(openDir, "session", "p1")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"id":"shared","directory":"/w/repo","title":"from opencode","time":{"updated":1000}}`
	if err := os.WriteFile(filepath.Join(sessions, "shared.json"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := &Options{ClaudeDir: claudeDir, OpencodeDir: openDir}
	got, err := loadSessions(t.Context(), opts)
	if err != nil {
		t.Fatalf("loadSessions() error = %v", err)
	}

	agents := map[string]int{}
	for _, s := range got {
		agents[s.Agent]++
		if s.ID != "shared" {
			t.Errorf("unexpected id %q", s.ID)
		}
	}
	if agents["claude"] != 1 || agents["opencode"] != 1 {
		t.Errorf("sessions by agent = %v, want one of each; a colliding id must not merge across agents", agents)
	}
}

// `list` writes to the same terminal the picker draws on, so a transcript's
// escape sequences reach it by this route too. The JSON form is left alone: the
// encoder escapes a control character on the way out, which is inert there and
// is data a consumer may legitimately want back.
func TestRunListWritesNoEscapeSequenceFromTheData(t *testing.T) {
	const escape, bell = rune(0x1b), rune(0x07)

	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "-tmp-repo")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	// An OSC 52 clipboard write, built from runes so this file holds no literal
	// control byte of its own.
	payload := "poc" + string(escape) + "]52;c;cHduZWQ=" + string(bell) + " done"
	line, err := json.Marshal(map[string]any{
		"type":      "user",
		"sessionId": "pocid",
		"cwd":       "/tmp/repo",
		"gitBranch": "main",
		"message":   map[string]any{"role": "user", "content": payload},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projects, "pocid.jsonl"), append(line, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	opts := &Options{ClaudeDir: dir, Limit: 10}
	cmd := newListCommand(opts, runList)
	cmd.SetArgs([]string{})
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	for _, r := range []rune{escape, bell} {
		if strings.ContainsRune(buf.String(), r) {
			t.Errorf("list wrote %#U from the data: %q", r, buf.String())
		}
	}
}
