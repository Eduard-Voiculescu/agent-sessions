package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func touchOlder(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}
}

func writeTranscript(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func userLine(cwd, branch, prompt string) string {
	return fmt.Sprintf(
		`{"type":"user","sessionId":"s1","cwd":%q,"gitBranch":%q,"timestamp":"2026-08-18T10:00:00.000Z","message":{"role":"user","content":%q}}`,
		cwd, branch, prompt,
	)
}

func titleLine(title string) string {
	return fmt.Sprintf(`{"type":"ai-title","aiTitle":%q,"sessionId":"s1"}`, title)
}

func TestParseTranscript(t *testing.T) {
	tests := []struct {
		name string
		body string
		want meta
	}{
		{
			name: "last ai-title wins",
			body: strings.Join([]string{
				titleLine("first guess"),
				userLine("/repo", "eng-3140-x", "do the thing"),
				titleLine("final title"),
			}, "\n") + "\n",
			want: meta{SessionID: "s1", Title: "final title", Cwd: "/repo", GitBranch: "eng-3140-x", FirstPrompt: "do the thing"},
		},
		{
			name: "no ai-title leaves the title empty",
			body: userLine("/repo", "develop", "just a prompt") + "\n",
			want: meta{SessionID: "s1", Cwd: "/repo", GitBranch: "develop", FirstPrompt: "just a prompt"},
		},
		{
			name: "malformed line is skipped",
			body: strings.Join([]string{
				"{not json at all",
				userLine("/repo", "develop", "still parsed"),
				titleLine("title"),
			}, "\n") + "\n",
			want: meta{SessionID: "s1", Title: "title", Cwd: "/repo", GitBranch: "develop", FirstPrompt: "still parsed"},
		},
		{
			name: "transcript with no user line still yields the title",
			body: strings.Join([]string{
				`{"type":"mode","mode":"normal","sessionId":"s1"}`,
				titleLine("title only"),
			}, "\n") + "\n",
			want: meta{SessionID: "s1", Title: "title only"},
		},
		{
			name: "first prompt takes the earliest user line",
			body: strings.Join([]string{
				userLine("/repo", "develop", "first"),
				userLine("/elsewhere", "other", "second"),
			}, "\n") + "\n",
			want: meta{SessionID: "s1", Cwd: "/repo", GitBranch: "develop", FirstPrompt: "first"},
		},
		{
			name: "a final line with no newline after it still applies",
			body: strings.Join([]string{
				userLine("/repo", "develop", "a prompt"),
				titleLine("last word"),
			}, "\n"),
			want: meta{SessionID: "s1", Title: "last word", Cwd: "/repo", GitBranch: "develop", FirstPrompt: "a prompt"},
		},
		{
			name: "a lone line with no newline after it still applies",
			body: userLine("/repo", "develop", "the only line"),
			want: meta{SessionID: "s1", Cwd: "/repo", GitBranch: "develop", FirstPrompt: "the only line"},
		},
		{
			name: "an empty cwd on the first user line is not a missing one",
			body: strings.Join([]string{
				userLine("", "", "first"),
				userLine("/repo", "develop", "second"),
			}, "\n") + "\n",
			want: meta{SessionID: "s1", FirstPrompt: "first"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTranscript(t, t.TempDir(), "s1.jsonl", tt.body)

			got, err := parseTranscript(path)
			if err != nil {
				t.Fatalf("parseTranscript() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("parseTranscript() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// A single hook attachment line of 464 KB was measured in real transcripts, and
// bufio.Scanner's default 64 KB token limit fails on it.
func TestParseTranscriptHandlesLineLongerThanHeadWindow(t *testing.T) {
	huge := fmt.Sprintf(`{"type":"attachment","sessionId":"s1","stdout":%q}`, strings.Repeat("x", 464<<10))
	body := strings.Join([]string{
		huge,
		userLine("/repo", "eng-3120-quick-actions", "after the giant attachment"),
		titleLine("survived"),
	}, "\n") + "\n"
	path := writeTranscript(t, t.TempDir(), "s1.jsonl", body)

	got, err := parseTranscript(path)
	if err != nil {
		t.Fatalf("parseTranscript() error = %v", err)
	}
	want := meta{
		SessionID:   "s1",
		Title:       "survived",
		Cwd:         "/repo",
		GitBranch:   "eng-3120-quick-actions",
		FirstPrompt: "after the giant attachment",
	}
	if got != want {
		t.Errorf("parseTranscript() = %+v, want %+v", got, want)
	}
}

// Files above wholeFileLimit are read as two windows; the title must come from
// the tail and the cwd from the head, and neither seam may yield a partial line.
func TestParseTranscriptSplitsHeadAndTailOnLargeFile(t *testing.T) {
	var b strings.Builder
	b.WriteString(userLine("/repo", "develop", "the very first prompt") + "\n")
	b.WriteString(titleLine("early title") + "\n")
	filler := fmt.Sprintf(`{"type":"assistant","sessionId":"s1","text":%q}`, strings.Repeat("y", 8<<10))
	for range 60 {
		b.WriteString(filler + "\n")
	}
	b.WriteString(titleLine("latest title") + "\n")

	path := writeTranscript(t, t.TempDir(), "s1.jsonl", b.String())
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Size() <= wholeFileLimit {
		t.Fatalf("fixture is %d bytes, want more than wholeFileLimit (%d)", info.Size(), wholeFileLimit)
	}

	got, err := parseTranscript(path)
	if err != nil {
		t.Fatalf("parseTranscript() error = %v", err)
	}
	if got.Title != "latest title" {
		t.Errorf("Title = %q, want %q", got.Title, "latest title")
	}
	if got.Cwd != "/repo" {
		t.Errorf("Cwd = %q, want %q", got.Cwd, "/repo")
	}
	if got.FirstPrompt != "the very first prompt" {
		t.Errorf("FirstPrompt = %q, want %q", got.FirstPrompt, "the very first prompt")
	}
}

func TestParseTranscriptDetectsSidechain(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "ordinary user line is not a sidechain",
			body: userLine("/repo", "develop", "a real prompt") + "\n",
			want: false,
		},
		{
			name: "sidechain user line is flagged",
			body: `{"type":"user","sessionId":"s1","cwd":"/repo","gitBranch":"develop","isSidechain":true,"message":{"role":"user","content":"dispatch prompt"}}` + "\n",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTranscript(t, t.TempDir(), "s1.jsonl", tt.body)

			got, err := parseTranscript(path)
			if err != nil {
				t.Fatalf("parseTranscript() error = %v", err)
			}
			if got.IsSidechain != tt.want {
				t.Errorf("IsSidechain = %v, want %v", got.IsSidechain, tt.want)
			}
		})
	}
}

func TestDiscoverWalksNestedDirectories(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, filepath.Join(root, "-Users-e-repo"), "a.jsonl", "{}\n")
	writeTranscript(t, filepath.Join(root, "-Users-e-repo--claude-worktrees-x"), "b.jsonl", "{}\n")
	writeTranscript(t, filepath.Join(root, "-Users-e-repo", "nested", "deeper"), "c.jsonl", "{}\n")
	writeTranscript(t, filepath.Join(root, "-Users-e-repo"), "notes.txt", "ignore me")

	got, err := discover(root)
	if err != nil {
		t.Fatalf("discover() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("discover() found %d transcripts, want 3: %+v", len(got), got)
	}
	for _, tr := range got {
		if filepath.Ext(tr.path) != ".jsonl" {
			t.Errorf("discover() returned %q, want only .jsonl files", tr.path)
		}
		if tr.modTime.IsZero() {
			t.Errorf("discover() returned zero modTime for %q", tr.path)
		}
	}
}

func TestDiscoverSkipsSubagentsDirectory(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, filepath.Join(root, "-Users-e-repo"), "real.jsonl", "{}\n")
	writeTranscript(t, filepath.Join(root, "-Users-e-repo", "subagents"), "dispatch.jsonl", "{}\n")

	got, err := discover(root)
	if err != nil {
		t.Fatalf("discover() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("discover() found %d transcripts, want 1 (subagents/ pruned): %+v", len(got), got)
	}
	if filepath.Base(got[0].path) != "real.jsonl" {
		t.Errorf("discover() returned %q, want real.jsonl", got[0].path)
	}
}

func TestDiscoverMissingRootIsNotAnError(t *testing.T) {
	got, err := discover(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("discover() error = %v, want nil for a missing root", err)
	}
	if len(got) != 0 {
		t.Errorf("discover() = %+v, want no transcripts", got)
	}
}

// The tail window ends at the real end of the file, so an unterminated last line
// there is a whole record too.
func TestParseTranscriptKeepsALargeFilesFinalLineWithoutANewline(t *testing.T) {
	var b strings.Builder
	b.WriteString(userLine("/repo", "develop", "the very first prompt") + "\n")
	filler := fmt.Sprintf(`{"type":"assistant","sessionId":"s1","text":%q}`, strings.Repeat("y", 8<<10))
	for range 60 {
		b.WriteString(filler + "\n")
	}
	b.WriteString(titleLine("unterminated title"))

	path := writeTranscript(t, t.TempDir(), "s1.jsonl", b.String())
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if info.Size() <= wholeFileLimit {
		t.Fatalf("fixture is %d bytes, want more than wholeFileLimit (%d)", info.Size(), wholeFileLimit)
	}

	got, err := parseTranscript(path)
	if err != nil {
		t.Fatalf("parseTranscript() error = %v", err)
	}
	if got.Title != "unterminated title" {
		t.Errorf("Title = %q, want %q", got.Title, "unterminated title")
	}
}

// A .jsonl under the projects directory is opened and its content drawn in the
// preview, so a link planted there would make the picker a reader for any file
// its owner can open.
func TestSymlinkedTranscriptsAreNeitherListedNorRead(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "projects", "p")

	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte(userLine("/x", "main", "leaked")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	real := writeTranscript(t, project, "real.jsonl", userLine("/x", "main", "fine")+"\n")

	link := filepath.Join(project, "linked.jsonl")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("this filesystem does not support symlinks: %v", err)
	}

	found, err := discover(filepath.Join(dir, "projects"))
	if err != nil {
		t.Fatalf("discover() error = %v", err)
	}
	for _, tr := range found {
		if tr.path == link {
			t.Errorf("discover() listed the symlink %s", link)
		}
	}
	if len(found) != 1 || found[0].path != real {
		t.Errorf("discover() = %+v, want only the real transcript %s", found, real)
	}

	if _, err := parseTranscript(link); err == nil {
		t.Error("parseTranscript() read through a symlink")
	}
	if _, err := readPreview(link); err == nil {
		t.Error("readPreview() read through a symlink")
	}
}
