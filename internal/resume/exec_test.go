package resume

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

type recorder struct {
	looked  string
	chdirTo string
	argv0   string
	argv    []string
}

func (r *recorder) options() []Option {
	return []Option{
		WithLookPath(func(name string) (string, error) {
			r.looked = name
			return "/usr/local/bin/" + name, nil
		}),
		WithChdir(func(dir string) error {
			r.chdirTo = dir
			return nil
		}),
		WithExec(func(argv0 string, argv, _ []string) error {
			r.argv0 = argv0
			r.argv = argv
			return nil
		}),
	}
}

func TestRunChdirsAndExecs(t *testing.T) {
	var rec recorder
	s := session.Session{Agent: "claude", ID: "abc", Cwd: "/Users/dev/repo"}
	argv := []string{"claude", "--resume", "abc"}

	if err := New(rec.options()...).Run(s, argv); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if rec.looked != "claude" {
		t.Errorf("LookPath called with %q, want %q", rec.looked, "claude")
	}
	if rec.chdirTo != "/Users/dev/repo" {
		t.Errorf("Chdir called with %q, want %q", rec.chdirTo, "/Users/dev/repo")
	}
	if rec.argv0 != "/usr/local/bin/claude" {
		t.Errorf("exec argv0 = %q, want %q", rec.argv0, "/usr/local/bin/claude")
	}
	if strings.Join(rec.argv, " ") != strings.Join(argv, " ") {
		t.Errorf("exec argv = %v, want %v", rec.argv, argv)
	}
}

func TestRunSkipsChdirWhenCwdIsUnknown(t *testing.T) {
	var rec recorder
	if err := New(rec.options()...).Run(session.Session{ID: "abc"}, []string{"claude", "--resume", "abc"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if rec.chdirTo != "" {
		t.Errorf("Chdir called with %q, want no call", rec.chdirTo)
	}
	if rec.argv0 == "" {
		t.Error("exec was never called, want the command to run from the current directory")
	}
}

func TestRunRejectsEmptyArgv(t *testing.T) {
	var rec recorder
	if err := New(rec.options()...).Run(session.Session{ID: "abc"}, nil); err == nil {
		t.Error("Run() error = nil, want an error for an empty argv")
	}
}

// missingBinary injects all three syscalls, not just the failing one: with the
// real os.Chdir and syscall.Exec left in place, a reordering that ran them before
// lookPath would replace the test binary instead of failing the test.
func missingBinary(t *testing.T) []Option {
	t.Helper()
	return []Option{
		WithLookPath(func(string) (string, error) { return "", exec.ErrNotFound }),
		WithChdir(func(dir string) error {
			t.Errorf("Chdir(%q) called even though the binary was never found", dir)
			return nil
		}),
		WithExec(func(argv0 string, _, _ []string) error {
			t.Errorf("Exec(%q) called even though the binary was never found", argv0)
			return nil
		}),
	}
}

func TestRunReportsTheCommandWhenBinaryIsMissing(t *testing.T) {
	runner := New(missingBinary(t)...)

	err := runner.Run(session.Session{ID: "abc"}, []string{"claude", "--resume", "abc"})
	if err == nil {
		t.Fatal("Run() error = nil, want an error when the binary is missing")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("Run() error = %v, want it to wrap exec.ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "claude --resume abc") {
		t.Errorf("Run() error = %q, want it to quote the intended command", err)
	}
}

func TestRunReturnsChdirFailure(t *testing.T) {
	boom := errors.New("no such directory")
	runner := New(
		WithLookPath(func(name string) (string, error) { return "/bin/" + name, nil }),
		WithChdir(func(string) error { return boom }),
		WithExec(func(string, []string, []string) error {
			t.Error("exec called after chdir failed")
			return nil
		}),
	)

	err := runner.Run(session.Session{ID: "abc", Cwd: "/gone"}, []string{"claude", "--resume", "abc"})
	if !errors.Is(err, boom) {
		t.Errorf("Run() error = %v, want it to wrap %v", err, boom)
	}
}

func TestRunLooksUpTheBinaryBeforeEnteringTheDirectory(t *testing.T) {
	runner := New(missingBinary(t)...)

	err := runner.Run(session.Session{ID: "abc", Cwd: "/Users/dev/repo"}, []string{"claude", "--resume", "abc"})
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("Run() error = %v, want it to wrap exec.ErrNotFound", err)
	}
}
