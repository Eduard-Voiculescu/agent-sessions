package cmd

import (
	"strings"
	"testing"
)

// subcommands is what the real tree carries, so a row naming any of them is
// something other than the picker.
var testSubcommands = []string{"list", "config", "purge", "watch", "jump", "help", "completion"}

func TestFindPickerPicksTheProcessWithNoSubcommand(t *testing.T) {
	out := strings.Join([]string{
		"43363       09:53 ??       agent-sessions watch",
		"33452 09-22:15:13 ttys014  agent-sessions",
		"  501       01:02 ttys001  /bin/zsh",
	}, "\n")

	got, err := findPicker(out, testSubcommands, 999)
	if err != nil {
		t.Fatalf("findPicker() error = %v, want nil", err)
	}
	if got != 33452 {
		t.Errorf("findPicker() = %d, want the picker's pid 33452", got)
	}
}

func TestFindPickerRejectsWhatIsNotAPicker(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		// The pet's own feed child. Without this the pet would jump to a process
		// that has no pane, on every double click.
		{name: "the feed child", line: "43363 09:53 ttys009 agent-sessions watch"},
		{name: "the jump doing the asking", line: "44000 00:01 ttys009 agent-sessions jump --picker"},
		{name: "a flag before the subcommand", line: "44100 00:01 ttys009 agent-sessions --live list"},
		// A process with no controlling terminal has no pane to focus, so
		// offering it would be a jump that always fails.
		{name: "no tty", line: "44200 09:53 ??      agent-sessions"},
		{name: "another program entirely", line: "44300 09:53 ttys009 agent-sessions-helper"},
		{name: "a pid that cannot be parsed", line: "notapid 09:53 ttys009 agent-sessions"},
		{name: "a row with nothing on it", line: "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := findPicker(tt.line, testSubcommands, 999); err == nil {
				t.Errorf("findPicker() = %d, want it refused", got)
			}
		})
	}
}

// The caller is an agent-sessions process too, and on the picker's own ctrl+j it
// carries no subcommand at all.
func TestFindPickerIgnoresTheProcessDoingTheAsking(t *testing.T) {
	out := "33452 09-22:15:13 ttys014  agent-sessions"

	if got, err := findPicker(out, testSubcommands, 33452); err == nil {
		t.Errorf("findPicker() = %d, want it to skip its own pid", got)
	}
}

// Newest wins: two pickers open means the second one is the one just reached
// for, and jumping to the older is jumping to the one already left behind.
func TestFindPickerTakesTheMostRecentlyStarted(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want int
	}{
		{
			name: "minutes against days",
			out: strings.Join([]string{
				"33452 09-22:15:13 ttys014  agent-sessions",
				"55555       02:31 ttys002  agent-sessions",
			}, "\n"),
			want: 55555,
		},
		{
			name: "hours against minutes",
			out: strings.Join([]string{
				"33452    04:10:00 ttys014  agent-sessions",
				"55555       59:59 ttys002  agent-sessions",
			}, "\n"),
			want: 55555,
		},
		{
			name: "days against days",
			out: strings.Join([]string{
				"33452 09-22:15:13 ttys014  agent-sessions",
				"55555 02-00:00:01 ttys002  agent-sessions",
			}, "\n"),
			want: 55555,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findPicker(tt.out, testSubcommands, 999)
			if err != nil {
				t.Fatalf("findPicker() error = %v, want nil", err)
			}
			if got != tt.want {
				t.Errorf("findPicker() = %d, want the newest %d", got, tt.want)
			}
		})
	}
}

// A picker started from an absolute path is the common case for an installed
// binary, and the pet's PATH lookup resolves to one.
func TestFindPickerAcceptsAnAbsolutePath(t *testing.T) {
	out := "33452 09:53 ttys014 /Users/dev/.local/bin/agent-sessions --live"

	got, err := findPicker(out, testSubcommands, 999)
	if err != nil {
		t.Fatalf("findPicker() error = %v, want nil", err)
	}
	if got != 33452 {
		t.Errorf("findPicker() = %d, want 33452", got)
	}
}

func TestFindPickerSaysWhenNothingIsRunning(t *testing.T) {
	_, err := findPicker("  501 01:02 ttys001 /bin/zsh", testSubcommands, 999)

	if err == nil || !strings.Contains(err.Error(), "picker") {
		t.Errorf("findPicker() error = %v, want it to say no picker is running", err)
	}
}
