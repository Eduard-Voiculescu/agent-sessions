package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

// liveRegistry writes one running-session record where the claude provider will
// find it. The pid must be this test process: a record whose process is not
// alive is dropped, which would make the fixture invisible and every assertion
// below vacuous.
func liveRegistry(t *testing.T, status string) string {
	t.Helper()

	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	body := `{"pid":` + strconv.Itoa(os.Getpid()) + `,"sessionId":"live-1","cwd":"/tmp/repo","name":"api","status":"` + status + `","updatedAt":1756300000000,"startedAt":1756290000000}`
	if err := os.WriteFile(filepath.Join(sessions, "live-1.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return dir
}

func watchOnce(t *testing.T, dir string) watchFrame {
	t.Helper()

	opts := &Options{ClaudeDir: dir, Limit: 10}
	cmd := newWatchCommand(opts)
	cmd.SetArgs([]string{"--once"})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("--once wrote %d lines, want 1: %q", len(lines), out.String())
	}

	var frame watchFrame
	if err := json.Unmarshal([]byte(lines[0]), &frame); err != nil {
		t.Fatalf("the feed line does not decode: %v\n%s", err, lines[0])
	}
	return frame
}

func TestWatchOnceEmitsOneClassifiedFrame(t *testing.T) {
	frame := watchOnce(t, liveRegistry(t, "waiting: input needed"))

	if frame.Worst != session.AttentionWaiting {
		t.Errorf("worst = %q, want %q", frame.Worst, session.AttentionWaiting)
	}
	if frame.Attention != 1 {
		t.Errorf("attention = %d, want 1", frame.Attention)
	}
	if len(frame.Sessions) != 1 || frame.Sessions[0].PID != os.Getpid() {
		t.Errorf("sessions = %+v, want the one live record", frame.Sessions)
	}
	if frame.At.IsZero() {
		t.Error("at is the zero time, want when the frame was built")
	}
}

func TestWatchOnceOnAnEmptyMachineSaysNone(t *testing.T) {
	frame := watchOnce(t, t.TempDir())

	if frame.Worst != session.AttentionNone {
		t.Errorf("worst = %q, want %q — an empty machine is the pet asleep, not an error", frame.Worst, session.AttentionNone)
	}
}

// The pet decodes every line, so a warning on stdout would be a protocol break.
func TestWatchKeepsDiagnosticsOffStdout(t *testing.T) {
	dir := liveRegistry(t, "busy")
	// A transcript directory that cannot be read makes Discover report a problem
	// while still returning the live record.
	unreadable := filepath.Join(dir, "projects", "-tmp-repo")
	if err := os.MkdirAll(unreadable, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	// Created readable and then closed: MkdirAll cannot make a child inside a
	// parent it has just made unreadable.
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() { os.Chmod(unreadable, 0o755) })

	opts := &Options{ClaudeDir: dir, Limit: 10}
	cmd := newWatchCommand(opts)
	cmd.SetArgs([]string{"--once"})
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, want the feed to survive a provider problem", err)
	}

	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		var frame watchFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Errorf("stdout carries something that is not a frame: %q", line)
		}
	}
}

func TestWatchRejectsANonPositiveInterval(t *testing.T) {
	opts := &Options{ClaudeDir: t.TempDir()}
	cmd := newWatchCommand(opts)
	cmd.SetArgs([]string{"--interval", "0s"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--interval") {
		t.Errorf("Execute() error = %v, want it to name --interval", err)
	}
}
