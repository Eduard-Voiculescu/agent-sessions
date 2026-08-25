package termjump_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/eduardvoiculescu/agent-sessions/internal/termjump"
)

func TestJumpBuildsScriptWithResolvedTTY(t *testing.T) {
	var gotScript string
	j := termjump.New(
		termjump.WithTTYFunc(func(context.Context, int) (string, error) { return "ttys002", nil }),
		termjump.WithRunFunc(func(_ context.Context, script string) (string, error) {
			gotScript = script
			return "FOUND", nil
		}),
	)

	if err := j.Jump(context.Background(), 18779); err != nil {
		t.Fatalf("Jump() error = %v, want nil", err)
	}
	if !strings.Contains(gotScript, "/dev/ttys002") {
		t.Errorf("script does not contain the resolved /dev/ttys002:\n%s", gotScript)
	}
}

// Jump runs two subprocesses that can each block on a human — the Automation
// consent dialog, an unresponsive iTerm2 — and it is called from bubbletea's
// event loop, so neither may run unbounded even when the caller hands over a
// context with no deadline of its own.
func TestJumpBoundsBothSubprocessesWithADeadline(t *testing.T) {
	var ttyDeadline, runDeadline bool
	j := termjump.New(
		termjump.WithTTYFunc(func(ctx context.Context, _ int) (string, error) {
			_, ttyDeadline = ctx.Deadline()
			return "ttys002", nil
		}),
		termjump.WithRunFunc(func(ctx context.Context, _ string) (string, error) {
			_, runDeadline = ctx.Deadline()
			return "FOUND", nil
		}),
	)

	if err := j.Jump(context.Background(), 7); err != nil {
		t.Fatalf("Jump() error = %v, want nil", err)
	}
	if !ttyDeadline {
		t.Error("the tty lookup ran with no deadline")
	}
	if !runDeadline {
		t.Error("the AppleScript ran with no deadline")
	}
}

func TestJumpReportsATimeoutAsSuch(t *testing.T) {
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	j := termjump.New(
		termjump.WithTTYFunc(func(context.Context, int) (string, error) { return "ttys002", nil }),
		termjump.WithRunFunc(func(ctx context.Context, _ string) (string, error) {
			return "", ctx.Err()
		}),
	)

	err := j.Jump(expired, 7)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("Jump() error = %v, want it to name the timeout rather than the killed subprocess", err)
	}
}

func TestJumpReportsNoControllingTerminal(t *testing.T) {
	ranScript := false
	j := termjump.New(
		termjump.WithTTYFunc(func(context.Context, int) (string, error) { return "??", nil }),
		termjump.WithRunFunc(func(context.Context, string) (string, error) {
			ranScript = true
			return "FOUND", nil
		}),
	)

	err := j.Jump(context.Background(), 4242)
	if err == nil {
		t.Fatal("Jump() error = nil, want an error for a pid with no controlling terminal")
	}
	if !strings.Contains(err.Error(), "4242") {
		t.Errorf("Jump() error = %q, want it to name the pid", err.Error())
	}
	if ranScript {
		t.Error("Jump() ran the AppleScript for a pid with no controlling terminal (\"??\")")
	}
}

func TestJumpWrapsAPSFailure(t *testing.T) {
	psErr := errors.New("ps: no such process")
	j := termjump.New(
		termjump.WithTTYFunc(func(context.Context, int) (string, error) { return "", psErr }),
		termjump.WithRunFunc(func(context.Context, string) (string, error) {
			t.Fatal("Jump() ran the AppleScript despite ps failing to resolve a tty")
			return "", nil
		}),
	)

	err := j.Jump(context.Background(), 999)
	if err == nil {
		t.Fatal("Jump() error = nil, want the ps failure surfaced")
	}
	if !errors.Is(err, psErr) {
		t.Errorf("Jump() error = %v, want it to wrap %v", err, psErr)
	}
}

func TestJumpReportsNoMatchingPaneAsAnError(t *testing.T) {
	j := termjump.New(
		termjump.WithTTYFunc(func(context.Context, int) (string, error) { return "ttys009", nil }),
		termjump.WithRunFunc(func(context.Context, string) (string, error) { return "NOT_FOUND", nil }),
	)

	err := j.Jump(context.Background(), 1234)
	if err == nil {
		t.Fatal("Jump() error = nil, want an error when no pane's tty matches")
	}
	if !strings.Contains(err.Error(), "1234") {
		t.Errorf("Jump() error = %q, want it to name the pid", err.Error())
	}
}

// "iTerm2 is not running" is the one failure the owner can act on directly, so
// it must not read like the walk that found no matching pane, nor like the
// platform check that runs before either.
func TestJumpDistinguishesITerm2StatesFromEachOther(t *testing.T) {
	messages := map[string]string{}
	for _, marker := range []string{"NOT_RUNNING", "NO_WINDOWS", "NOT_FOUND"} {
		j := termjump.New(
			termjump.WithTTYFunc(func(context.Context, int) (string, error) { return "ttys002", nil }),
			termjump.WithRunFunc(func(context.Context, string) (string, error) { return marker, nil }),
		)

		err := j.Jump(context.Background(), 1234)
		if err == nil {
			t.Fatalf("Jump() error = nil for %s, want an error", marker)
		}
		messages[marker] = err.Error()
	}

	if !strings.Contains(messages["NOT_RUNNING"], "not running") {
		t.Errorf("NOT_RUNNING reported as %q, want it to say iTerm2 is not running", messages["NOT_RUNNING"])
	}
	if !strings.Contains(messages["NO_WINDOWS"], "no windows") {
		t.Errorf("NO_WINDOWS reported as %q, want it to say iTerm2 has no windows", messages["NO_WINDOWS"])
	}
	seen := map[string]bool{}
	for marker, msg := range messages {
		if seen[msg] {
			t.Errorf("%s reports the same message as another cause: %q", marker, msg)
		}
		seen[msg] = true
	}
}

func TestJumpWrapsARunFailure(t *testing.T) {
	runErr := errors.New("osascript: command not found")
	j := termjump.New(
		termjump.WithTTYFunc(func(context.Context, int) (string, error) { return "ttys002", nil }),
		termjump.WithRunFunc(func(context.Context, string) (string, error) { return "", runErr }),
	)

	err := j.Jump(context.Background(), 1234)
	if err == nil {
		t.Fatal("Jump() error = nil, want the run failure surfaced")
	}
	if !errors.Is(err, runErr) {
		t.Errorf("Jump() error = %v, want it to wrap %v", err, runErr)
	}
}

// The order of select statements is critical: a tab remembers its active session,
// so selecting the tab after the session would reset the session selection. The order
// must be window, tab, session (outermost to innermost) to ensure the final selection sticks.
func TestJumpSelectsInCorrectOrder(t *testing.T) {
	var gotScript string
	j := termjump.New(
		termjump.WithTTYFunc(func(context.Context, int) (string, error) { return "ttys009", nil }),
		termjump.WithRunFunc(func(_ context.Context, script string) (string, error) {
			gotScript = script
			return "FOUND", nil
		}),
	)

	if err := j.Jump(context.Background(), 5678); err != nil {
		t.Fatalf("Jump() error = %v, want nil", err)
	}

	posW := strings.Index(gotScript, "select w")
	posT := strings.Index(gotScript, "select t")
	posS := strings.Index(gotScript, "select s")

	if posW < 0 {
		t.Error("script missing 'select w'")
	}
	if posT < 0 {
		t.Error("script missing 'select t'")
	}
	if posS < 0 {
		t.Error("script missing 'select s'")
	}

	if posW > 0 && posT > 0 && posW >= posT {
		t.Errorf("'select w' at position %d should appear before 'select t' at position %d", posW, posT)
	}
	if posT > 0 && posS > 0 && posT >= posS {
		t.Errorf("'select t' at position %d should appear before 'select s' at position %d", posT, posS)
	}
}

func TestSendWritesTheMessageIntoTheResolvedPane(t *testing.T) {
	var gotScript string
	j := termjump.New(
		termjump.WithTTYFunc(func(context.Context, int) (string, error) { return "ttys007", nil }),
		termjump.WithRunFunc(func(_ context.Context, script string) (string, error) {
			gotScript = script
			return "FOUND", nil
		}),
	)

	if err := j.Send(context.Background(), 4242, "run the tests"); err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	if !strings.Contains(gotScript, `(tty of s) is "/dev/ttys007"`) {
		t.Errorf("script does not match on the resolved tty:\n%s", gotScript)
	}
	if !strings.Contains(gotScript, `tell s to write text "run the tests"`) {
		t.Errorf("script does not write the message into the matched session:\n%s", gotScript)
	}
}

// Sending must not steal focus: the whole point of typing into the pane rather
// than jumping to it is that the picker stays on screen and shows the reply
// arriving. select and activate are jump's business alone.
func TestSendNeitherSelectsNorActivates(t *testing.T) {
	var gotScript string
	j := termjump.New(
		termjump.WithTTYFunc(func(context.Context, int) (string, error) { return "ttys002", nil }),
		termjump.WithRunFunc(func(_ context.Context, script string) (string, error) {
			gotScript = script
			return "FOUND", nil
		}),
	)

	if err := j.Send(context.Background(), 7, "hello"); err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	for _, banned := range []string{"activate", "select w", "select t", "select s"} {
		if strings.Contains(gotScript, banned) {
			t.Errorf("send script contains %q, which moves the owner's focus:\n%s", banned, gotScript)
		}
	}
}

// A message is typed by a person, so it is the one interpolation in this
// package that an attacker-shaped string can reach. The quote must not be able
// to close the literal: were it copied through raw, everything after it would
// be read as AppleScript, and `display dialog` here stands in for anything
// worse. Asserting the payload is absent is not enough — it appears escaped in
// a correct script too — so this pins the escaped form and the shape of the
// statement around it.
func TestSendEscapesAMessageThatTriesToCloseTheStringLiteral(t *testing.T) {
	payload := `bye" 
	display dialog "pwned`
	// The raw payload has a newline, which Send refuses outright.
	if err := termjump.New().Send(context.Background(), 7, payload); err == nil {
		t.Error("Send() accepted a message containing a newline")
	}

	oneLine := `bye" & (display dialog "pwned") & "`
	var gotScript string
	j := termjump.New(
		termjump.WithTTYFunc(func(context.Context, int) (string, error) { return "ttys002", nil }),
		termjump.WithRunFunc(func(_ context.Context, script string) (string, error) {
			gotScript = script
			return "FOUND", nil
		}),
	)

	if err := j.Send(context.Background(), 7, oneLine); err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}

	want := `tell s to write text "bye\" & (display dialog \"pwned\") & \""`
	if !strings.Contains(gotScript, want) {
		t.Errorf("script does not carry the message as one fully escaped literal:\nwant substring %s\ngot:\n%s", want, gotScript)
	}
	// Every double quote in the write statement past the opening one must be
	// backslash-escaped, so the literal cannot end early.
	stmt := gotScript[strings.Index(gotScript, "tell s to write text"):]
	stmt = stmt[:strings.Index(stmt, "\n")]
	if strings.Count(stmt, `\"`) != strings.Count(stmt, `"`)-2 {
		t.Errorf("the write statement has an unescaped quote, so the literal can be closed:\n%s", stmt)
	}
}

func TestSendRefusesInputItCannotFaithfullyType(t *testing.T) {
	j := termjump.New(
		termjump.WithTTYFunc(func(context.Context, int) (string, error) { return "ttys002", nil }),
		termjump.WithRunFunc(func(context.Context, string) (string, error) { return "FOUND", nil }),
	)

	for _, tt := range []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"whitespace only", "   \t "},
		{"embedded newline", "first\nsecond"},
		{"escape sequence", "clear\x1b[2J"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := j.Send(context.Background(), 7, tt.text); err == nil {
				t.Errorf("Send(%q) error = nil, want a refusal", tt.text)
			}
		})
	}
}

// Backslashes survive as backslashes: a Windows-style path or a regex in a
// message must arrive as typed, not as whatever AppleScript decodes the
// unescaped form into.
func TestSendEscapesBackslashes(t *testing.T) {
	var gotScript string
	j := termjump.New(
		termjump.WithTTYFunc(func(context.Context, int) (string, error) { return "ttys002", nil }),
		termjump.WithRunFunc(func(_ context.Context, script string) (string, error) {
			gotScript = script
			return "FOUND", nil
		}),
	)

	if err := j.Send(context.Background(), 7, `grep '\d+' .`); err != nil {
		t.Fatalf("Send() error = %v, want nil", err)
	}
	if !strings.Contains(gotScript, `write text "grep '\\d+' ."`) {
		t.Errorf("backslash was not escaped:\n%s", gotScript)
	}
}

// Send reports the pane failures through the same translation Jump does, so a
// message aimed at a pane that has gone away says so rather than silently
// doing nothing.
func TestSendReportsPaneFailures(t *testing.T) {
	for _, tt := range []struct {
		marker string
		want   string
	}{
		{"NOT_FOUND", "no iTerm2 pane found"},
		{"NOT_RUNNING", "iTerm2 is not running"},
		{"NO_WINDOWS", "no windows open"},
	} {
		t.Run(tt.marker, func(t *testing.T) {
			j := termjump.New(
				termjump.WithTTYFunc(func(context.Context, int) (string, error) { return "ttys002", nil }),
				termjump.WithRunFunc(func(context.Context, string) (string, error) { return tt.marker, nil }),
			)
			err := j.Send(context.Background(), 7, "hi")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Send() error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}
