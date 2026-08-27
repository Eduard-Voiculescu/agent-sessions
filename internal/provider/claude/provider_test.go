package claude

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

func TestResumeArgv(t *testing.T) {
	tests := []struct {
		name string
		s    session.Session
		fork bool
		want []string
	}{
		{
			name: "resume by id",
			s:    session.Session{ID: "abc"},
			want: []string{"claude", "--resume", "abc"},
		},
		{
			name: "fork adds the flag",
			s:    session.Session{ID: "abc"},
			fork: true,
			want: []string{"claude", "--resume", "abc", "--fork-session"},
		},
		{
			name: "no id falls back to the transcript path",
			s:    session.Session{Transcript: "/t/a.jsonl"},
			want: []string{"claude", "--resume", "/t/a.jsonl"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(t.TempDir()).ResumeArgv(tt.s, tt.fork)
			if err != nil {
				t.Fatalf("ResumeArgv() error = %v", err)
			}
			if strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Errorf("ResumeArgv() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResumeArgvWithoutIdOrTranscript(t *testing.T) {
	if _, err := New(t.TempDir()).ResumeArgv(session.Session{}, false); err == nil {
		t.Error("ResumeArgv() error = nil, want an error when there is nothing to resume")
	}
}

// The separator is load-bearing, not decoration. Claude Code takes the prompt
// positionally, so verified against the real binary: `claude --resume <id> -p
// "-v"` prints the version and resumes nothing, while the same call with `--`
// ahead of the prompt reaches the session lookup. Any message beginning with a
// dash — a diff line, a flag someone is asking about — hits that.
func TestResumeWithPromptArgvSeparatesThePromptFromTheFlags(t *testing.T) {
	got, err := New(t.TempDir()).ResumeWithPromptArgv(session.Session{ID: "abc"}, "-v is what?")
	if err != nil {
		t.Fatalf("ResumeWithPromptArgv() error = %v", err)
	}

	want := []string{"claude", "--resume", "abc", "--", "-v is what?"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("ResumeWithPromptArgv() = %v, want %v", got, want)
	}

	separator := slices.Index(got, "--")
	if separator < 0 {
		t.Fatal("argv has no -- separator, so a prompt starting with a dash is parsed as a flag")
	}
	if separator != len(got)-2 {
		t.Errorf("the separator is at %d of %d, want it immediately before the prompt", separator, len(got))
	}
}

func TestResumeWithPromptArgvRejectsAnEmptyPrompt(t *testing.T) {
	for _, prompt := range []string{"", "   ", "\t"} {
		if _, err := New(t.TempDir()).ResumeWithPromptArgv(session.Session{ID: "abc"}, prompt); err == nil {
			t.Errorf("ResumeWithPromptArgv(%q) error = nil, want a refusal", prompt)
		}
	}
}

// A prompt does not imply a fork: sending a message continues the session the
// preview was opened on, and --fork-session would branch it into a copy instead.
func TestResumeWithPromptArgvDoesNotFork(t *testing.T) {
	got, err := New(t.TempDir()).ResumeWithPromptArgv(session.Session{ID: "abc"}, "carry on")
	if err != nil {
		t.Fatalf("ResumeWithPromptArgv() error = %v", err)
	}
	if slices.Contains(got, "--fork-session") {
		t.Errorf("ResumeWithPromptArgv() = %v, want no fork flag", got)
	}
}

func TestDiscoverBuildsSessionsFromTranscripts(t *testing.T) {
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "-Users-e-repo")
	writeTranscript(t, projects, "s1.jsonl", strings.Join([]string{
		userLine("/Users/dev/repo", "eng-3140-x", "the prompt"),
		titleLine("the title"),
	}, "\n")+"\n")

	got, err := New(dir).Discover(context.Background(), 10)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Discover() returned %d sessions, want 1: %+v", len(got), got)
	}

	s := got[0]
	if s.Agent != "claude" {
		t.Errorf("Agent = %q, want %q", s.Agent, "claude")
	}
	if s.ID != "s1" {
		t.Errorf("ID = %q, want %q", s.ID, "s1")
	}
	if s.Name != "the title" {
		t.Errorf("Name = %q, want %q", s.Name, "the title")
	}
	if s.Title != "the title" {
		t.Errorf("Title = %q, want %q", s.Title, "the title")
	}
	if s.Cwd != "/Users/dev/repo" {
		t.Errorf("Cwd = %q, want %q", s.Cwd, "/Users/dev/repo")
	}
	if s.GitBranch != "eng-3140-x" {
		t.Errorf("GitBranch = %q, want %q", s.GitBranch, "eng-3140-x")
	}
	if s.Live {
		t.Error("Live = true, want false for a history-only session")
	}
}

func TestDiscoverNameFallsBackToPromptThenShortID(t *testing.T) {
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "-Users-e-repo")
	writeTranscript(t, projects, "8c4d5e6f-0000-4000-8000-000000000004.jsonl",
		userLine("/Users/dev/repo", "develop", "a prompt with no ai title")+"\n")
	writeTranscript(t, projects, "5e1f2a3b-0000-4000-8000-000000000001.jsonl",
		`{"type":"mode","mode":"normal","sessionId":"5e1f2a3b-0000-4000-8000-000000000001"}`+"\n")

	got, err := New(dir).Discover(context.Background(), 10)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	names := map[string]string{}
	for _, s := range got {
		names[s.ID] = s.Name
	}
	if names["8c4d5e6f-0000-4000-8000-000000000004"] != "a prompt with no ai title" {
		t.Errorf("prompt fallback = %q, want the prompt text", names["8c4d5e6f-0000-4000-8000-000000000004"])
	}
	if names["5e1f2a3b-0000-4000-8000-000000000001"] != "5e1f2a3b" {
		t.Errorf("short-id fallback = %q, want %q", names["5e1f2a3b-0000-4000-8000-000000000001"], "5e1f2a3b")
	}
}

func TestDiscoverNameStripsClaudeCodeWrapperMarkup(t *testing.T) {
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "-Users-e-repo")
	writeTranscript(t, projects, "caveat.jsonl",
		userLine("/repo", "develop", "<local-command-caveat>Caveat: the messages below were generated by the user while running a local command.</local-command-caveat>")+"\n")
	writeTranscript(t, projects, "wrapped.jsonl",
		userLine("/repo", "develop", "<command-message>ledger-query-threads</command-message>")+"\n")
	writeTranscript(t, projects, "plain.jsonl",
		userLine("/repo", "develop", "fix the flaky test")+"\n")

	got, err := New(dir).Discover(context.Background(), 10)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	names := map[string]string{}
	for _, s := range got {
		names[s.ID] = s.Name
	}
	if names["caveat"] != "caveat" {
		t.Errorf("local-command-caveat prompt name = %q, want the short id %q — the caveat carries no user intent", names["caveat"], "caveat")
	}
	if names["wrapped"] != "ledger-query-threads" {
		t.Errorf("wrapped prompt name = %q, want the tag stripped down to the command name", names["wrapped"])
	}
	if names["plain"] != "fix the flaky test" {
		t.Errorf("plain prompt name = %q, want it passed through untouched", names["plain"])
	}
}

func TestDiscoverHonoursLimitTakingNewestFirst(t *testing.T) {
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "-Users-e-repo")
	for _, name := range []string{"old.jsonl", "new.jsonl"} {
		writeTranscript(t, projects, name, userLine("/repo", "develop", name)+"\n")
	}
	touchOlder(t, filepath.Join(projects, "old.jsonl"))

	got, err := New(dir).Discover(context.Background(), 1)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Discover() returned %d sessions, want 1", len(got))
	}
	if got[0].ID != "new" {
		t.Errorf("Discover() kept %q, want the newest transcript %q", got[0].ID, "new")
	}
}

func TestDiscoverOmitsSidechainTranscripts(t *testing.T) {
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "-Users-e-repo")
	writeTranscript(t, projects, "real.jsonl", userLine("/Users/dev/repo", "develop", "a real prompt")+"\n")
	writeTranscript(t, projects, "sidechain.jsonl",
		`{"type":"user","sessionId":"sidechain","cwd":"/Users/dev/repo","gitBranch":"develop","isSidechain":true,"message":{"role":"user","content":"dispatch prompt"}}`+"\n")

	got, err := New(dir).Discover(context.Background(), 10)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Discover() returned %d sessions, want 1 (sidechain excluded): %+v", len(got), got)
	}
	if got[0].ID != "real" {
		t.Errorf("Discover() kept %q, want the real transcript %q", got[0].ID, "real")
	}
}

func TestLiveUsesInjectedAliveFunc(t *testing.T) {
	dir := t.TempDir()
	writeRegistry(t, dir, "58854.json", liveEntry)

	got, err := New(dir, WithAliveFunc(func(int) bool { return false })).Live(context.Background())
	if err != nil {
		t.Fatalf("Live() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Live() = %+v, want nothing when no pid is alive", got)
	}
}

func TestDiscoverCountsUnreadableTranscripts(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000-mode file, so the failure cannot be provoked")
	}

	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "-Users-e-repo")
	writeTranscript(t, projects, "good.jsonl", userLine("/Users/dev/repo", "develop", "readable")+"\n")
	unreadable := writeTranscript(t, projects, "bad.jsonl", userLine("/Users/dev/repo", "develop", "sealed")+"\n")
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}

	got, err := New(dir).Discover(context.Background(), 10)
	if err == nil {
		t.Error("Discover() error = nil, want the unreadable transcript reported")
	}
	if len(got) != 1 {
		t.Errorf("Discover() returned %d sessions, want the readable one kept: %+v", len(got), got)
	}
}

func TestActionsContributesADeleteAction(t *testing.T) {
	got := New(t.TempDir()).Actions()
	if len(got) != 1 {
		t.Fatalf("Actions() returned %d actions, want 1", len(got))
	}
	if got[0].ID() != "session.delete" {
		t.Errorf("Actions()[0].ID() = %q, want %q", got[0].ID(), "session.delete")
	}
}

// A session id is a transcript's filename or a field out of its JSON, so it is
// whatever wrote the file says it is. `claude --resume --fork-session` is that
// flag rather than that id.
func TestResumeArgvRefusesATargetThatWouldParseAsAFlag(t *testing.T) {
	p := New(t.TempDir())

	for _, tt := range []struct {
		name    string
		s       session.Session
		wantErr bool
	}{
		{"a uuid", session.Session{ID: "0f8c1a3e-2b41-4d7a-9c6e-11f2a3b4c5d6"}, false},
		{"a transcript path", session.Session{Transcript: "/Users/x/.claude/projects/p/a.jsonl"}, false},
		{"a flag as an id", session.Session{ID: "--dangerously-skip-permissions"}, true},
		{"a short flag as an id", session.Session{ID: "-v"}, true},
		{"an escape in an id", session.Session{ID: "id\x1b]52;c;x\x07"}, true},
		{"neither an id nor a transcript", session.Session{}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			argv, err := p.ResumeArgv(tt.s, false)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResumeArgv(%+v) = %q, %v; wantErr %v", tt.s, argv, err, tt.wantErr)
			}

			withPrompt, err := p.ResumeWithPromptArgv(tt.s, "hello")
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResumeWithPromptArgv(%+v) = %q, %v; wantErr %v", tt.s, withPrompt, err, tt.wantErr)
			}
		})
	}
}

// The guard has to sit where the argv is built, not in the picker: the same argv
// is rendered into the pasteable command the copy.resume action writes to the
// clipboard, and quoting protects the shell without protecting the agent that
// parses what the shell hands it.
func TestDiscoveredSessionsCannotSmuggleAFlagOutOfAFilename(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, filepath.Join(dir, "projects", "-Users-x-repo"),
		"--dangerously-skip-permissions.jsonl",
		userLine("/Users/x/repo", "main", "hello")+"\n")

	p := New(dir, WithAliveFunc(func(int) bool { return false }))
	found, err := p.Discover(context.Background(), 0)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("Discover() found %d sessions, want 1", len(found))
	}
	if _, err := p.ResumeArgv(found[0], false); err == nil {
		t.Errorf("ResumeArgv() accepted the id %q read from a filename", found[0].ID)
	}
}

func TestStartArgvStartsClaudeFresh(t *testing.T) {
	got := New(t.TempDir()).StartArgv()

	want := []string{"claude"}
	if !slices.Equal(got, want) {
		t.Errorf("StartArgv() = %v, want %v", got, want)
	}
}
