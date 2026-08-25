package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eduardvoiculescu/agent-sessions/internal/action"
	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

func trashFile(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	root := filepath.Join(action.TrashRoot(dir), "-Users-e-repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}
	return path
}

func TestPurgeWithoutFlagsDeletesNothing(t *testing.T) {
	dir := t.TempDir()
	path := trashFile(t, dir, "old.jsonl", 90*24*time.Hour)

	out := &bytes.Buffer{}
	if err := runPurge(out, &Options{ClaudeDir: dir}, purgeOptions{}); err != nil {
		t.Fatalf("runPurge() error = %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Error("a bare purge deleted a file; it must only report")
	}
	if !strings.Contains(out.String(), "old.jsonl") {
		t.Errorf("output = %q, want it to list what would be deleted", out.String())
	}
}

func TestPurgeOlderThanDeletesOnlyOldEnoughFiles(t *testing.T) {
	dir := t.TempDir()
	old := trashFile(t, dir, "old.jsonl", 40*24*time.Hour)
	fresh := trashFile(t, dir, "fresh.jsonl", time.Hour)

	if err := runPurge(&bytes.Buffer{}, &Options{ClaudeDir: dir}, purgeOptions{olderThan: 30 * 24 * time.Hour}); err != nil {
		t.Fatalf("runPurge() error = %v", err)
	}

	if _, err := os.Stat(old); err == nil {
		t.Error("old.jsonl survived --older-than 30d")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh.jsonl was deleted by --older-than 30d")
	}
}

func TestPurgeDryRunDeletesNothing(t *testing.T) {
	dir := t.TempDir()
	path := trashFile(t, dir, "old.jsonl", 40*24*time.Hour)

	out := &bytes.Buffer{}
	if err := runPurge(out, &Options{ClaudeDir: dir}, purgeOptions{olderThan: time.Hour, dryRun: true}); err != nil {
		t.Fatalf("runPurge() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("--dry-run deleted a file")
	}

	// --older-than was passed here, so the closing line must not tell the user
	// to pass it; that wording belongs to the bare-invocation path only.
	if !strings.Contains(out.String(), "dry run") {
		t.Errorf("output = %q, want the closing line to say this was a dry run", out.String())
	}
	if strings.Contains(out.String(), "pass --older-than") {
		t.Errorf("output = %q, want it not to ask for a flag that was already given", out.String())
	}
}

func TestPurgeNegativeOlderThanErrorsAndDeletesNothing(t *testing.T) {
	dir := t.TempDir()
	old := trashFile(t, dir, "old.jsonl", 40*24*time.Hour)
	fresh := trashFile(t, dir, "fresh.jsonl", time.Hour)

	err := runPurge(&bytes.Buffer{}, &Options{ClaudeDir: dir}, purgeOptions{olderThan: -24 * time.Hour})
	if err == nil {
		t.Fatal("runPurge() error = nil, want a negative --older-than rejected")
	}

	if _, err := os.Stat(old); err != nil {
		t.Error("old.jsonl was deleted despite the negative --older-than being rejected")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh.jsonl was deleted despite the negative --older-than being rejected")
	}
}

func TestPurgeReportsErrorWhenADeletionFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so the failure cannot be provoked")
	}

	dir := t.TempDir()
	root := filepath.Join(action.TrashRoot(dir), "-Users-e-repo")
	path := trashFile(t, dir, "old.jsonl", 40*24*time.Hour)

	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	defer func() {
		if err := os.Chmod(root, 0o755); err != nil {
			t.Fatalf("restoring directory mode: %v", err)
		}
	}()

	out := &bytes.Buffer{}
	err := runPurge(out, &Options{ClaudeDir: dir}, purgeOptions{olderThan: time.Hour})
	if err == nil {
		t.Fatal("runPurge() error = nil, want the failed deletion surfaced")
	}
	if !strings.Contains(out.String(), "failed") {
		t.Errorf("output = %q, want the failure count reported", out.String())
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Error("file unexpectedly removed even though its directory is read-only")
	}
}

func TestPurgeWarnsOnUnreadableProjectDirButStillPurgesTheRest(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so the failure cannot be provoked")
	}

	dir := t.TempDir()
	readable := trashFile(t, dir, "readable.jsonl", 40*24*time.Hour)

	blockedRoot := filepath.Join(action.TrashRoot(dir), "-Users-e-blocked")
	if err := os.MkdirAll(blockedRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	blockedPath := filepath.Join(blockedRoot, "blocked.jsonl")
	if err := os.WriteFile(blockedPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	when := time.Now().Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(blockedPath, when, when); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	if err := os.Chmod(blockedRoot, 0o000); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(blockedRoot, 0o700); err != nil {
			t.Fatalf("restoring directory mode: %v", err)
		}
	})

	out := &bytes.Buffer{}
	err := runPurge(out, &Options{ClaudeDir: dir}, purgeOptions{olderThan: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("runPurge() error = %v, want nil since a warning is not a run failure", err)
	}

	if _, statErr := os.Stat(readable); statErr == nil {
		t.Error("readable.jsonl survived even though it was old enough to purge")
	}
	if !strings.Contains(out.String(), "warning:") {
		t.Errorf("output = %q, want a warning about the unreadable directory", out.String())
	}
	if !strings.Contains(out.String(), blockedRoot) {
		t.Errorf("output = %q, want the warning to name %s", out.String(), blockedRoot)
	}
}

func TestPurgeWithoutFlagsAdvisesOlderThan(t *testing.T) {
	dir := t.TempDir()
	trashFile(t, dir, "old.jsonl", 90*24*time.Hour)

	out := &bytes.Buffer{}
	if err := runPurge(out, &Options{ClaudeDir: dir}, purgeOptions{}); err != nil {
		t.Fatalf("runPurge() error = %v", err)
	}

	if !strings.Contains(out.String(), "pass --older-than") {
		t.Errorf("output = %q, want a bare purge to advise passing --older-than", out.String())
	}
	if strings.Contains(out.String(), "dry run") {
		t.Errorf("output = %q, want the bare-invocation wording, not the dry-run wording", out.String())
	}
}

// TestPurgeSpareTranscriptTrashedFromAnOldSession drives the delete action and
// runPurge against one directory. A session idle for 90 days keeps that mtime
// through os.Rename, so without a deletion stamp its recovery window in the
// trash is zero: the purge a user runs to clear old cruft destroys what they
// deleted seconds earlier.
func TestPurgeSpareTranscriptTrashedFromAnOldSession(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "projects", "-Users-e-repo", "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(transcript, []byte(`{"type":"user"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	stale := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(transcript, stale, stale); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	opts := &Options{ClaudeDir: dir}
	s := session.Session{Agent: "claude", ID: "s1", Name: "s1", Transcript: transcript}
	var del action.Action
	for _, a := range actionsFor(opts, registryFor(opts))(s) {
		if a.ID() == "session.delete" {
			del = a
		}
	}
	if del == nil {
		t.Fatal("no session.delete action for a claude session")
	}
	if _, err := del.Run(context.Background(), s); err != nil {
		t.Fatalf("delete action Run() error = %v", err)
	}

	trashed := filepath.Join(action.TrashRoot(dir), "-Users-e-repo", "s1.jsonl")
	if _, err := os.Stat(trashed); err != nil {
		t.Fatalf("transcript missing from the trash at %s: %v", trashed, err)
	}

	out := &bytes.Buffer{}
	if err := runPurge(out, opts, purgeOptions{olderThan: 720 * time.Hour}); err != nil {
		t.Fatalf("runPurge() error = %v", err)
	}
	if _, err := os.Stat(trashed); err != nil {
		t.Fatalf("purge --older-than 720h deleted a transcript trashed a moment ago: %v\n%s", err, out)
	}
}

func TestPurgeListingCarriesTheRestoreCommand(t *testing.T) {
	dir := t.TempDir()
	path := trashFile(t, dir, "old.jsonl", 90*24*time.Hour)

	out := &bytes.Buffer{}
	if err := runPurge(out, &Options{ClaudeDir: dir}, purgeOptions{}); err != nil {
		t.Fatalf("runPurge() error = %v", err)
	}

	// The picker's footer has no room for a full mv command, so this listing is
	// where a user who regrets a delete has to be able to read one.
	if !strings.Contains(out.String(), action.RestoreCommand(dir, path)) {
		t.Errorf("output = %q, want the restore command for %s", out.String(), path)
	}
}
