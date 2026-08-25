package action

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

func writeTranscript(t *testing.T, dir, project, name string) string {
	t.Helper()
	full := filepath.Join(dir, "projects", project)
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	path := filepath.Join(full, name)
	if err := os.WriteFile(path, []byte(`{"type":"user"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func TestTrashDeleteMovesTranscript(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "-Users-e-repo", "s1.jsonl")
	stale := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}
	act := NewTrashDelete(TrashConfig{ClaudeDir: dir, Alive: func(int) bool { return false }})
	s := session.Session{Agent: "claude", ID: "s1", Name: "s1", Transcript: path}

	res, err := act.Run(context.Background(), s)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("original transcript still present after delete")
	}
	moved := filepath.Join(TrashRoot(dir), "-Users-e-repo", "s1.jsonl")
	info, err := os.Stat(moved)
	if err != nil {
		t.Fatalf("trashed file missing at %s: %v", moved, err)
	}
	// os.Rename carries the transcript's mtime over, and purge measures the
	// recovery window from this stamp: unstamped, a session idle for 90 days is
	// already past --older-than 720h the moment it is deleted.
	if age := time.Since(info.ModTime()); age > time.Minute {
		t.Errorf("trashed file's mtime is %s old, want the deletion time; purge --older-than would destroy it at once", age)
	}
	if !strings.Contains(res.Status, "s1.jsonl") {
		t.Errorf("Status = %q, want it to name the file that was trashed", res.Status)
	}
	if !res.Refresh {
		t.Error("Result.Refresh = false; the deleted row must disappear from the list")
	}
}

// The footer is capped at the row table's span — 78 columns on an 80-column
// terminal — and truncation eats the tail, so what has to survive is the file's
// name and the command that finds it again.
func TestTrashDeleteStatusStaysReadableInTheFooter(t *testing.T) {
	const footerSpan = 77

	dir := t.TempDir()
	path := writeTranscript(t, dir, "-Users-e-a-very-long-project-slug-from-a-deep-directory", "0e8f1b2c-3d4e-5f60-7182-93a4b5c6d7e8.jsonl")
	act := NewTrashDelete(TrashConfig{ClaudeDir: dir, Alive: func(int) bool { return false }})

	res, err := act.Run(context.Background(), session.Session{ID: "s1", Transcript: path})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	head := res.Status
	if len(head) > footerSpan {
		head = head[:footerSpan]
	}
	if !strings.Contains(head, filepath.Base(path)) {
		t.Errorf("the readable part of the status is %q, want it to name %s", head, filepath.Base(path))
	}
	if !strings.Contains(head, "agent-sessions purge") {
		t.Errorf("the readable part of the status is %q, want it to name the command that lists the trash", head)
	}
}

func TestTrashDeleteStampsTheDeletionTimeOnBothMovePaths(t *testing.T) {
	tests := []struct {
		name   string
		rename func(string, string) error
	}{
		{name: "rename", rename: os.Rename},
		{name: "copy fallback", rename: func(string, string) error { return &os.LinkError{Err: syscall.EXDEV} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeTranscript(t, dir, "-Users-e-repo", "s1.jsonl")
			var stamped []string
			act := NewTrashDelete(TrashConfig{
				ClaudeDir: dir,
				Rename:    tt.rename,
				Chtimes: func(p string, _, _ time.Time) error {
					stamped = append(stamped, p)
					return nil
				},
				Alive: func(int) bool { return false },
			})

			if _, err := act.Run(context.Background(), session.Session{ID: "s1", Transcript: path}); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			moved := filepath.Join(TrashRoot(dir), "-Users-e-repo", "s1.jsonl")
			if !slices.Contains(stamped, moved) {
				t.Errorf("stamped = %v, want the trashed file %q", stamped, moved)
			}
		})
	}
}

// A stamp that fails leaves the trash's age wrong, but the transcript has
// already moved: reporting a failure would strand the deleted row on the list
// with nothing said about where its file went.
func TestTrashDeleteStillReportsSuccessWhenTheStampFails(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "-Users-e-repo", "s1.jsonl")
	act := NewTrashDelete(TrashConfig{
		ClaudeDir: dir,
		Chtimes:   func(string, time.Time, time.Time) error { return errors.New("read-only file system") },
		Alive:     func(int) bool { return false },
	})

	res, err := act.Run(context.Background(), session.Session{ID: "s1", Transcript: path})
	if err != nil {
		t.Fatalf("Run() error = %v, want the move still reported as done", err)
	}
	if !res.Refresh {
		t.Error("Result.Refresh = false; the deleted row would stay on the list")
	}
	if !strings.Contains(res.Status, "read-only file system") {
		t.Errorf("Status = %q, want the stamp failure mentioned rather than swallowed", res.Status)
	}
	// The footer truncates at the row table's width, so a warning appended after
	// the reassuring half is a warning the user never sees — which is C1's own
	// failure mode wearing a different hat.
	head := res.Status
	if len(head) > 77 {
		head = head[:77]
	}
	if !strings.HasPrefix(head, "WARNING:") || !strings.Contains(head, "may be purged early") {
		t.Errorf("the readable part of the status is %q, want the warning and its consequence to lead it", head)
	}
}

func TestRestoreCommandSurvivesAShellRoundTripWithSpaces(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "-Users-e-My Project", "s1.jsonl")
	act := NewTrashDelete(TrashConfig{ClaudeDir: dir, Alive: func(int) bool { return false }})

	if _, err := act.Run(context.Background(), session.Session{ID: "s1", Transcript: path}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	trashed := filepath.Join(TrashRoot(dir), "-Users-e-My Project", "s1.jsonl")
	mv := RestoreCommand(dir, trashed)
	if out, err := exec.Command("/bin/sh", "-c", mv).CombinedOutput(); err != nil {
		t.Fatalf("restore command %q failed: %v\n%s", mv, err, out)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("restored transcript missing at %s: %v", path, err)
	}
	if string(got) != `{"type":"user"}`+"\n" {
		t.Errorf("restored transcript contents = %q, want the original", got)
	}
}

func TestTrashDeleteKeepsSameIdFromDifferentProjectsApart(t *testing.T) {
	dir := t.TempDir()
	first := writeTranscript(t, dir, "-Users-e-alpha", "same.jsonl")
	second := writeTranscript(t, dir, "-Users-e-beta", "same.jsonl")
	act := NewTrashDelete(TrashConfig{ClaudeDir: dir, Alive: func(int) bool { return false }})

	for _, path := range []string{first, second} {
		if _, err := act.Run(context.Background(), session.Session{ID: "same", Transcript: path}); err != nil {
			t.Fatalf("Run(%s) error = %v", path, err)
		}
	}

	for _, project := range []string{"-Users-e-alpha", "-Users-e-beta"} {
		if _, err := os.Stat(filepath.Join(TrashRoot(dir), project, "same.jsonl")); err != nil {
			t.Errorf("trashed file for %s missing: %v", project, err)
		}
	}
}

func TestTrashDeleteWalksPastAnOccupiedSlot(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "-Users-e-repo", "s1.jsonl")
	act := NewTrashDelete(TrashConfig{ClaudeDir: dir, Alive: func(int) bool { return false }})
	s := session.Session{ID: "s1", Transcript: path}

	if _, err := act.Run(context.Background(), s); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"assistant"}`+"\n"), 0o600); err != nil {
		t.Fatalf("recreating transcript: %v", err)
	}
	if _, err := act.Run(context.Background(), s); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}

	first := filepath.Join(TrashRoot(dir), "-Users-e-repo", "s1.jsonl")
	firstContent, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("first trashed copy missing at %s: %v", first, err)
	}
	if string(firstContent) != `{"type":"user"}`+"\n" {
		t.Errorf("first trashed copy contents = %q, want the original delete's contents unchanged", firstContent)
	}

	second := filepath.Join(TrashRoot(dir), "-Users-e-repo", "s1-2.jsonl")
	secondContent, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("second trashed copy missing at %s: %v", second, err)
	}
	if string(secondContent) != `{"type":"assistant"}`+"\n" {
		t.Errorf("second trashed copy contents = %q, want the recreated transcript's contents", secondContent)
	}
}

func TestRestoreCommandDeclinesToClobberTheNameItLeft(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "-Users-e-repo", "s1.jsonl")
	act := NewTrashDelete(TrashConfig{ClaudeDir: dir, Alive: func(int) bool { return false }})

	if _, err := act.Run(context.Background(), session.Session{ID: "s1", Transcript: path}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"assistant"}`+"\n"), 0o600); err != nil {
		t.Fatalf("recreating transcript: %v", err)
	}

	// Stdin is closed so mv -i's overwrite prompt sees EOF and declines, which is
	// what proves -i (not just quoting) is doing the work: without it the restore
	// would silently destroy the session that took the name back.
	mv := RestoreCommand(dir, filepath.Join(TrashRoot(dir), "-Users-e-repo", "s1.jsonl"))
	cmd := exec.Command("/bin/sh", "-c", mv)
	cmd.Stdin = strings.NewReader("")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("restore command %q failed: %v\n%s", mv, err, out)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("transcript missing at %s: %v", path, err)
	}
	if string(got) != `{"type":"assistant"}`+"\n" {
		t.Errorf("contents = %q, want the recreated transcript to survive the restore", got)
	}
}

func TestTrashDeleteRefusesATranscriptWithNoProjectDirectory(t *testing.T) {
	act := NewTrashDelete(TrashConfig{ClaudeDir: t.TempDir(), Alive: func(int) bool { return false }})
	s := session.Session{ID: "s1", Transcript: "justafile.jsonl"}

	_, err := act.Run(context.Background(), s)
	if err == nil {
		t.Fatal("Run() error = nil, want a refusal when the transcript has no project directory")
	}
	if !strings.Contains(err.Error(), "cannot derive a trash path") {
		t.Errorf("error = %q, want the trash-path guard's message", err)
	}
}

func TestTrashDeleteFallsBackToCopyAcrossDevices(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "-Users-e-repo", "s1.jsonl")
	copied := false
	act := NewTrashDelete(TrashConfig{
		ClaudeDir: dir,
		Rename:    func(string, string) error { return &os.LinkError{Err: syscall.EXDEV} },
		Copy: func(old, new string) error {
			copied = true
			data, err := os.ReadFile(old)
			if err != nil {
				return err
			}
			return os.WriteFile(new, data, 0o600)
		},
		Alive: func(int) bool { return false },
	})

	if _, err := act.Run(context.Background(), session.Session{ID: "s1", Transcript: path}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !copied {
		t.Error("copy fallback was not used after an EXDEV rename")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("original still present after the copy fallback")
	}
}

func TestTrashDeleteRemovesPartialCopyOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "-Users-e-repo", "s1.jsonl")
	target := filepath.Join(TrashRoot(dir), "-Users-e-repo", "s1.jsonl")
	var removed []string
	act := NewTrashDelete(TrashConfig{
		ClaudeDir: dir,
		Rename:    func(string, string) error { return &os.LinkError{Err: syscall.EXDEV} },
		Copy:      func(string, string) error { return errors.New("disk full") },
		Remove:    func(p string) error { removed = append(removed, p); return nil },
		Alive:     func(int) bool { return false },
	})

	if _, err := act.Run(context.Background(), session.Session{ID: "s1", Transcript: path}); err == nil {
		t.Fatal("Run() error = nil, want the copy failure surfaced")
	}
	if !slices.Contains(removed, target) {
		t.Errorf("removed = %v, want it to contain the partial copy %q", removed, target)
	}
	if slices.Contains(removed, path) {
		t.Errorf("removed = %v, must never contain the original transcript %q", removed, path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("original transcript was removed even though the copy failed")
	}
}

func TestTrashDeleteIsNotOfferedWhileTheSessionIsLive(t *testing.T) {
	act := NewTrashDelete(TrashConfig{ClaudeDir: t.TempDir(), Alive: func(int) bool { return true }})
	s := session.Session{ID: "s1", Transcript: "/t/-Users-e-repo/s1.jsonl", Live: true, PID: 42}

	if act.Available(s) {
		t.Error("Available() = true for a live session; the entry would spend a confirmation on a guaranteed refusal")
	}
	if !act.Available(session.Session{ID: "s1", Transcript: "/t/-Users-e-repo/s1.jsonl"}) {
		t.Error("Available() = false for a history row that has a transcript")
	}
}

func TestTrashDeleteRefusesWhileTheSessionIsLive(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "-Users-e-repo", "s1.jsonl")
	act := NewTrashDelete(TrashConfig{ClaudeDir: dir, Alive: func(int) bool { return true }})
	s := session.Session{ID: "s1", Transcript: path, Live: true, PID: 42}

	_, err := act.Run(context.Background(), s)
	if err == nil {
		t.Fatal("Run() error = nil, want a refusal while the process is running")
	}
	if !strings.Contains(err.Error(), "kill") {
		t.Errorf("error = %q, want it to suggest killing the process first", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("transcript was moved despite the session being live")
	}
}

func TestTrashDeleteConfirmsFirst(t *testing.T) {
	act := NewTrashDelete(TrashConfig{ClaudeDir: t.TempDir(), Alive: func(int) bool { return false }})
	if act.Confirm(session.Session{Name: "webapp-8e"}) == "" {
		t.Error("Confirm() is empty; deleting a transcript must ask first")
	}
}

func TestTrashedFiles(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(TrashRoot(dir), "-Users-e-repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.jsonl"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := TrashedFiles(dir)
	if err != nil {
		t.Fatalf("TrashedFiles() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("TrashedFiles() = %+v, want 1 entry", got)
	}
	if got[0].ModTime.IsZero() {
		t.Error("entry has a zero ModTime; purge --older-than would misbehave")
	}
}

func TestTrashedFilesReportsAnUnreadableProjectDirectoryButKeepsTheRest(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000-mode directory, so the failure cannot be provoked")
	}

	dir := t.TempDir()
	good := filepath.Join(TrashRoot(dir), "-Users-e-good")
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(good, "a.jsonl"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	sealed := filepath.Join(TrashRoot(dir), "-Users-e-sealed")
	if err := os.MkdirAll(sealed, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sealed, "b.jsonl"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.Chmod(sealed, 0o000); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	defer func() {
		if err := os.Chmod(sealed, 0o755); err != nil {
			t.Fatalf("restoring directory mode: %v", err)
		}
	}()

	got, err := TrashedFiles(dir)
	if err == nil {
		t.Error("TrashedFiles() error = nil, want the unreadable directory reported")
	}
	if len(got) != 1 || got[0].Path != filepath.Join(good, "a.jsonl") {
		t.Errorf("TrashedFiles() = %+v, want only the readable entry kept", got)
	}
}

func TestTrashedFilesMissingRootIsNotAnError(t *testing.T) {
	got, err := TrashedFiles(t.TempDir())
	if err != nil {
		t.Fatalf("TrashedFiles() error = %v, want nil when nothing was ever trashed", err)
	}
	if len(got) != 0 {
		t.Errorf("TrashedFiles() = %+v, want empty", got)
	}
}

// The trash holds whole conversations — pasted credentials, file contents, tool
// output. Its directory must not be readable by another account on the machine,
// and os.Rename carries the transcript's own mode across, so the mode has to be
// set after the move rather than inherited from wherever the transcript lived.
func TestTrashKeepsWhatItHoldsPrivate(t *testing.T) {
	claude := t.TempDir()
	project := filepath.Join(claude, "projects", "p")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(project, "abc.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := NewTrashDelete(TrashConfig{ClaudeDir: claude, Alive: func(int) bool { return false }})
	if _, err := a.Run(context.Background(), session.Session{Name: "s", Transcript: transcript}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	trashed, err := TrashedFiles(claude)
	if err != nil {
		t.Fatalf("TrashedFiles() error = %v", err)
	}
	if len(trashed) != 1 {
		t.Fatalf("trash holds %d files, want 1", len(trashed))
	}

	info, err := os.Stat(trashed[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != trashFileMode {
		t.Errorf("trashed transcript mode is %04o, want %04o", perm, trashFileMode)
	}

	dir, err := os.Stat(filepath.Dir(trashed[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dir.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("trash directory mode is %04o, which another account can read", perm)
	}
}

// Deleting the same session twice must not replace the first copy: the trash is
// the only place a regretted delete can be recovered from, and os.Rename
// overwrites without a word.
func TestTrashNeverOverwritesAnEntryItAlreadyHolds(t *testing.T) {
	claude := t.TempDir()
	project := filepath.Join(claude, "projects", "p")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	a := NewTrashDelete(TrashConfig{ClaudeDir: claude, Alive: func(int) bool { return false }})
	for _, body := range []string{"first\n", "second\n"} {
		transcript := filepath.Join(project, "abc.jsonl")
		if err := os.WriteFile(transcript, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Run(context.Background(), session.Session{Name: "s", Transcript: transcript}); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}

	trashed, err := TrashedFiles(claude)
	if err != nil {
		t.Fatalf("TrashedFiles() error = %v", err)
	}
	if len(trashed) != 2 {
		t.Fatalf("trash holds %d files, want 2", len(trashed))
	}

	bodies := map[string]bool{}
	for _, f := range trashed {
		raw, err := os.ReadFile(f.Path)
		if err != nil {
			t.Fatal(err)
		}
		bodies[string(raw)] = true
	}
	if !bodies["first\n"] || !bodies["second\n"] {
		t.Errorf("trash holds %v, want both deletes kept", bodies)
	}
}

// claim reserves the name rather than checking whether it is free, so the
// walk-past cannot race a second delete into the same slot.
func TestClaimReservesTheNameItReturns(t *testing.T) {
	dir := t.TempDir()
	a := trashAction{cfg: TrashConfig{ClaudeDir: dir}}
	base := filepath.Join(dir, "abc.jsonl")

	first, err := a.claim(base)
	if err != nil {
		t.Fatalf("claim() error = %v", err)
	}
	if first != base {
		t.Errorf("claim() = %q, want the base name %q", first, base)
	}
	if _, err := os.Stat(first); err != nil {
		t.Errorf("claim() did not create %q: %v", first, err)
	}

	second, err := a.claim(base)
	if err != nil {
		t.Fatalf("claim() error = %v", err)
	}
	if second == first {
		t.Errorf("claim() returned %q twice", second)
	}
	if filepath.Ext(second) != ".jsonl" {
		t.Errorf("claim() = %q, want the .jsonl extension kept so purge still lists it", second)
	}
}
