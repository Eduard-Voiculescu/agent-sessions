package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func runJumpArgs(t *testing.T, opts *Options, args []string, jump func(context.Context, int) error) error {
	t.Helper()

	return runJumpWithPicker(t, opts, args, jump, func(context.Context) (int, error) {
		return 0, errors.New("no agent-sessions picker is running")
	})
}

func runJumpWithPicker(t *testing.T, opts *Options, args []string, jump func(context.Context, int) error, picker func(context.Context) (int, error)) error {
	t.Helper()

	cmd := newJumpCommand(opts, jump, picker)
	cmd.SetArgs(args)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	return cmd.Execute()
}

func TestJumpPassesThePidThrough(t *testing.T) {
	got := 0
	err := runJumpArgs(t, &Options{}, []string{"--pid", "4242"}, func(_ context.Context, pid int) error {
		got = pid
		return nil
	})

	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if got != 4242 {
		t.Errorf("jump got pid %d, want 4242", got)
	}
}

func TestJumpReportsTheJumpsOwnFailure(t *testing.T) {
	err := runJumpArgs(t, &Options{}, []string{"--pid", "7"}, func(context.Context, int) error {
		return errors.New("iTerm2 is not running")
	})

	if err == nil || !strings.Contains(err.Error(), "iTerm2 is not running") {
		t.Errorf("Execute() error = %v, want the reason passed through", err)
	}
}

func TestJumpRefusesWhatItCannotAimAt(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "neither", args: nil, want: "--pid"},
		{name: "both", args: []string{"--pid", "7", "--session", "abc"}, want: "one of"},
		{name: "zero pid", args: []string{"--pid", "0"}, want: "--pid"},
		{name: "negative pid", args: []string{"--pid", "-3"}, want: "positive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ran := false
			err := runJumpArgs(t, &Options{}, tt.args, func(context.Context, int) error {
				ran = true
				return nil
			})

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Execute() error = %v, want it to mention %q", err, tt.want)
			}
			if ran {
				t.Error("the jump ran anyway")
			}
		})
	}
}

func TestJumpResolvesASessionIDToItsPid(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	body := `{"pid":` + strconv.Itoa(os.Getpid()) + `,"sessionId":"abc123","cwd":"/tmp/repo","name":"api","status":"busy","updatedAt":1756300000000,"startedAt":1756290000000}`
	if err := os.WriteFile(filepath.Join(sessions, "abc123.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got := 0
	err := runJumpArgs(t, &Options{ClaudeDir: dir, Limit: 10}, []string{"--session", "abc123"}, func(_ context.Context, pid int) error {
		got = pid
		return nil
	})

	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if got != os.Getpid() {
		t.Errorf("jump got pid %d, want the record's %d", got, os.Getpid())
	}
}

func TestJumpSaysWhenASessionIDIsNotRunning(t *testing.T) {
	err := runJumpArgs(t, &Options{ClaudeDir: t.TempDir(), Limit: 10}, []string{"--session", "nope"}, func(context.Context, int) error {
		return nil
	})

	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("Execute() error = %v, want it to name the id it could not find", err)
	}
}

func TestJumpPickerAimsAtThePickerItFound(t *testing.T) {
	got := 0
	err := runJumpWithPicker(t, &Options{}, []string{"--picker"},
		func(_ context.Context, pid int) error {
			got = pid
			return nil
		},
		func(context.Context) (int, error) { return 33452, nil },
	)

	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if got != 33452 {
		t.Errorf("jump got pid %d, want the picker's 33452", got)
	}
}

// The pet swallows this one: a double click with no picker open does nothing,
// which is what was asked for. It still has to be an error rather than a jump to
// pid zero.
func TestJumpPickerReportsThatNoneIsRunning(t *testing.T) {
	ran := false
	err := runJumpWithPicker(t, &Options{}, []string{"--picker"},
		func(context.Context, int) error {
			ran = true
			return nil
		},
		func(context.Context) (int, error) { return 0, errors.New("no agent-sessions picker is running") },
	)

	if err == nil || !strings.Contains(err.Error(), "picker") {
		t.Errorf("Execute() error = %v, want it to say no picker is running", err)
	}
	if ran {
		t.Error("the jump ran anyway")
	}
}

func TestJumpRefusesPickerAlongsideAnotherTarget(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "with a pid", args: []string{"--picker", "--pid", "7"}},
		{name: "with a session", args: []string{"--picker", "--session", "abc"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ran := false
			err := runJumpWithPicker(t, &Options{}, tt.args,
				func(context.Context, int) error {
					ran = true
					return nil
				},
				func(context.Context) (int, error) { return 33452, nil },
			)

			if err == nil || !strings.Contains(err.Error(), "one of") {
				t.Errorf("Execute() error = %v, want it to refuse two targets", err)
			}
			if ran {
				t.Error("the jump ran anyway")
			}
		})
	}
}
