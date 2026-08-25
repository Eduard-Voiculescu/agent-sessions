package termjump

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// A pid that has just exited is the common case for jumping at a stale row, and
// ps reports it with exit status 1 and nothing on either stream: without this
// mapping the owner reads "resolving tty for pid 7: exit status 1", which names
// no cause at all.
func TestDefaultTTYMapsAnExitedPidToErrProcessGone(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("running the throwaway process: %v", err)
	}

	_, err := defaultTTY(context.Background(), cmd.Process.Pid)
	if !errors.Is(err, errProcessGone) {
		t.Errorf("defaultTTY(pid of an exited process) error = %v, want errProcessGone", err)
	}
}

// A ps that fails for its own reasons must keep its diagnostic: the mapping
// above may only claim a dead pid when ps said nothing at all.
func TestDefaultTTYKeepsPSsOwnDiagnostic(t *testing.T) {
	_, err := defaultTTY(context.Background(), 1<<30)
	if err == nil {
		t.Fatal("defaultTTY() error = nil, want ps to reject an out-of-range pid")
	}
	if errors.Is(err, errProcessGone) {
		t.Errorf("defaultTTY() error = %v, want the ps diagnostic rather than a dead-pid claim", err)
	}
	if !strings.Contains(err.Error(), "process id") {
		t.Errorf("defaultTTY() error = %q, want ps's own message in it", err.Error())
	}
}

// exec.ExitError.Error() prints only the status, so every osascript failure —
// the denied Automation consent dialog above all — arrives as "exit status 1"
// with the one sentence that explains it sitting unread in Stderr.
func TestWithStderrAppendsTheDiagnostic(t *testing.T) {
	_, err := exec.Command("sh", "-c", "echo 'not authorised to send Apple events' >&2; exit 1").Output()
	if err == nil {
		t.Fatal("the failing command reported no error")
	}
	if strings.Contains(err.Error(), "Apple events") {
		t.Fatal("ExitError already prints its stderr; withStderr no longer has a job")
	}

	got := withStderr(err).Error()
	if !strings.Contains(got, "not authorised to send Apple events") {
		t.Errorf("withStderr() = %q, want the diagnostic appended", got)
	}
	if !strings.Contains(got, "exit status 1") {
		t.Errorf("withStderr() = %q, want the exit status kept alongside it", got)
	}
}

func TestWithStderrLeavesOtherErrorsAlone(t *testing.T) {
	own := errors.New("exec: \"osascript\": executable file not found in $PATH")
	if got := withStderr(own); !errors.Is(got, own) {
		t.Errorf("withStderr() = %v, want the original error", got)
	}
}
