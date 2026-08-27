package opencode

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

// writeSession puts one record where Discover will find it: storage/session/
// <projectID>/ses_*.json, the layout opencode itself uses.
func writeSession(t *testing.T, root, project, id, body string) string {
	t.Helper()
	dir := filepath.Join(root, "session", project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The record shape is copied from a real file rather than invented, so a field
// rename upstream shows up here as a failure rather than as an empty column.
const realRecord = `{
  "id": "ses_44c779bffffe6s3L616EE5d0Fs",
  "version": "1.1.14",
  "projectID": "67796c03c5c85778edd37a3d0daece9f37fe065a",
  "directory": "/Users/dev/git/misc/BMAD-METHOD",
  "title": "Understanding the BMad Method in current repository",
  "time": { "created": 1768243618816, "updated": 1768244917085 },
  "summary": { "additions": 0, "deletions": 0, "files": 0 }
}`

func TestDiscoverMapsARealRecord(t *testing.T) {
	root := t.TempDir()
	path := writeSession(t, root, "67796c03", "ses_44c779bffffe6s3L616EE5d0Fs", realRecord)

	got, err := New(root).Discover(t.Context(), 0)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Discover() returned %d sessions, want 1", len(got))
	}

	s := got[0]
	if s.Agent != "opencode" {
		t.Errorf("Agent = %q, want opencode", s.Agent)
	}
	if s.ID != "ses_44c779bffffe6s3L616EE5d0Fs" {
		t.Errorf("ID = %q", s.ID)
	}
	if s.Name != "Understanding the BMad Method in current repository" || s.Title != s.Name {
		t.Errorf("Name = %q, Title = %q", s.Name, s.Title)
	}
	if s.Cwd != "/Users/dev/git/misc/BMAD-METHOD" {
		t.Errorf("Cwd = %q", s.Cwd)
	}
	if s.Transcript != path {
		t.Errorf("Transcript = %q, want the session file %q", s.Transcript, path)
	}
	// time.updated, in milliseconds. Read as seconds it would land in 57000 AD
	// and sort above everything forever.
	if want := time.UnixMilli(1768244917085); !s.LastActive.Equal(want) {
		t.Errorf("LastActive = %v, want %v", s.LastActive, want)
	}
	// opencode does not record a branch, and inventing one per row by shelling
	// out to git would be worse than leaving the column empty.
	if s.GitBranch != "" {
		t.Errorf("GitBranch = %q, want empty", s.GitBranch)
	}
	if s.Live || s.PID != 0 || s.Status != "" {
		t.Errorf("liveness was set on a provider that publishes none: %+v", s)
	}
}

// The filter that has to be exact. A child session is a conversation nobody
// held directly; listing one puts a plausible-looking row in the picker that
// resumes into the middle of somebody else's sub-agent. Asserted by id, not by
// count: a count passes when the wrong record is dropped.
func TestDiscoverSkipsChildSessions(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "p1", "ses_parent", `{"id":"ses_parent","directory":"/w","title":"held by a person","time":{"updated":2000}}`)
	writeSession(t, root, "p1", "ses_child", `{"id":"ses_child","parentID":"ses_parent","directory":"/w","title":"spawned","time":{"updated":3000}}`)

	got, err := New(root).Discover(t.Context(), 0)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	ids := make([]string, 0, len(got))
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	if slices.Contains(ids, "ses_child") {
		t.Errorf("Discover() = %v, want the child session skipped", ids)
	}
	if !slices.Contains(ids, "ses_parent") {
		t.Errorf("Discover() = %v, want the parent session kept", ids)
	}
}

// session/global holds records whose projectID is the literal "global". They
// carry a real directory and are read like any other.
func TestDiscoverReadsTheGlobalProject(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "global", "ses_g", `{"id":"ses_g","directory":"/w/repo","title":"global one","time":{"updated":1000}}`)

	got, err := New(root).Discover(t.Context(), 0)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 || got[0].Cwd != "/w/repo" {
		t.Errorf("Discover() = %+v, want the global project's session with its directory", got)
	}
}

// A record with no timestamp must not sort below every dated session forever,
// which is what the zero time would do — it reads as the session having
// vanished rather than as one whose time is unknown.
func TestDiscoverFallsBackToTheFileTimeNotTheZeroTime(t *testing.T) {
	root := t.TempDir()
	path := writeSession(t, root, "p1", "ses_undated", `{"id":"ses_undated","directory":"/w","title":"no time at all"}`)

	got, err := New(root).Discover(t.Context(), 0)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Discover() returned %d sessions, want 1", len(got))
	}
	if got[0].LastActive.IsZero() {
		t.Fatal("LastActive is the zero time; the session sorts below everything forever")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].LastActive.Equal(info.ModTime()) {
		t.Errorf("LastActive = %v, want the file's mtime %v", got[0].LastActive, info.ModTime())
	}
}

// created stands in when updated is absent, rather than falling through to the
// file time: the record does carry a timestamp.
func TestDiscoverUsesCreatedWhenUpdatedIsMissing(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "p1", "ses_c", `{"id":"ses_c","directory":"/w","title":"t","time":{"created":1768243618816}}`)

	got, err := New(root).Discover(t.Context(), 0)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if want := time.UnixMilli(1768243618816); !got[0].LastActive.Equal(want) {
		t.Errorf("LastActive = %v, want created %v", got[0].LastActive, want)
	}
}

// One unreadable file costs its own row and not the scan, matching how the
// Claude provider counts unreadable transcripts.
func TestDiscoverReportsBadRecordsAndKeepsTheRest(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "p1", "ses_good", `{"id":"ses_good","directory":"/w","title":"fine","time":{"updated":1000}}`)
	writeSession(t, root, "p1", "ses_torn", `{"id":"ses_torn","direc`)
	writeSession(t, root, "p1", "ses_noid", `{"directory":"/w","title":"no id"}`)

	got, err := New(root).Discover(t.Context(), 0)
	if err == nil {
		t.Error("Discover() error = nil, want the unreadable records reported")
	}
	if len(got) != 1 || got[0].ID != "ses_good" {
		t.Errorf("Discover() = %+v, want the readable record kept", got)
	}
}

// opencode may not be installed, which is not a failure to report.
func TestDiscoverToleratesNoStorage(t *testing.T) {
	got, err := New(filepath.Join(t.TempDir(), "absent")).Discover(t.Context(), 0)
	if err != nil {
		t.Errorf("Discover() error = %v, want nil when opencode is not installed", err)
	}
	if len(got) != 0 {
		t.Errorf("Discover() = %+v, want nothing", got)
	}
}

func TestDiscoverIgnoresNonSessionFiles(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "p1", "ses_real", `{"id":"ses_real","directory":"/w","title":"t","time":{"updated":1000}}`)
	if err := os.WriteFile(filepath.Join(root, "session", "p1", "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "migration"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := New(root).Discover(t.Context(), 0)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 {
		t.Errorf("Discover() returned %d sessions, want only the one .json record", len(got))
	}
}

// limit keeps the newest, so raising it can only add older rows and never
// change which sessions were already on screen.
func TestDiscoverLimitKeepsTheNewest(t *testing.T) {
	root := t.TempDir()
	for i, ms := range []int64{1000, 5000, 3000} {
		id := string(rune('a' + i))
		writeSession(t, root, "p1", "ses_"+id,
			`{"id":"ses_`+id+`","directory":"/w","title":"t","time":{"updated":`+itoa(ms)+`}}`)
	}

	got, err := New(root).Discover(t.Context(), 1)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Discover() returned %d sessions, want 1", len(got))
	}
	if want := time.UnixMilli(5000); !got[0].LastActive.Equal(want) {
		t.Errorf("kept the session from %v, want the newest %v", got[0].LastActive, want)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestResumeArgv(t *testing.T) {
	for _, tt := range []struct {
		name string
		fork bool
		want []string
	}{
		{"resume", false, []string{"opencode", "--session", "ses_x"}},
		{"fork", true, []string{"opencode", "--session", "ses_x", "--fork"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(t.TempDir()).ResumeArgv(session.Session{ID: "ses_x"}, tt.fork)
			if err != nil {
				t.Fatalf("ResumeArgv() error = %v", err)
			}
			if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
				t.Errorf("ResumeArgv() = %v, want %v", got, tt.want)
			}
			// --fork is documented only alongside --session or --continue, so it
			// must never be the only thing on the line.
			if tt.fork && !slices.Contains(got, "--session") {
				t.Errorf("ResumeArgv() = %v, want --fork paired with --session", got)
			}
		})
	}

	if _, err := New(t.TempDir()).ResumeArgv(session.Session{}, false); err == nil {
		t.Error("ResumeArgv() error = nil, want a refusal with no id")
	}
}

func TestDirHonoursXDGDataHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/xdg/data")
	if got, want := Dir(), filepath.Join("/xdg/data", "opencode", "storage"); got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}

	t.Setenv("XDG_DATA_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got, want := Dir(), filepath.Join(home, ".local", "share", "opencode", "storage"); got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestResumeArgvRefusesAnIDThatWouldParseAsAFlag(t *testing.T) {
	p := New(t.TempDir())

	for _, tt := range []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"an opencode id", "ses_7Kq2mZ", false},
		{"a flag as an id", "--print-logs", true},
		{"an escape in an id", "ses\x1b]52;c;x\x07", true},
		{"empty", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			argv, err := p.ResumeArgv(session.Session{ID: tt.id}, false)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResumeArgv(%q) = %q, %v; wantErr %v", tt.id, argv, err, tt.wantErr)
			}
		})
	}
}

func TestStartArgvStartsOpencodeFresh(t *testing.T) {
	got := New(t.TempDir()).StartArgv()

	want := []string{"opencode"}
	if !slices.Equal(got, want) {
		t.Errorf("StartArgv() = %v, want %v", got, want)
	}
}
