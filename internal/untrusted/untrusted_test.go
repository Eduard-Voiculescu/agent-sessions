package untrusted

import (
	"strings"
	"testing"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

func TestTextNeutralisesEverySequenceATerminalActsOn(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
		want  string
	}{
		{"plain text is untouched", "already safe", "already safe"},
		{"escape", "a\x1bb", "a b"},
		// Only the ESC and the BEL are characters the terminal acts on. What is
		// left of the sequence stays as ordinary text, which is the point: the
		// terminal never enters OSC mode to read it.
		{"osc 52 clipboard write", "hi\x1b]52;c;cHduZWQ=\x07", "hi ]52;c;cHduZWQ= "},
		{"sgr colour", "a\x1b[31mb", "a [31mb"},
		{"carriage return, which redraws the line", "safe\rEVIL", "safe EVIL"},
		{"delete", "a\x7fb", "a b"},
		{"c1 introducer", "a\u009bb", "a b"},
		{"unicode line separator", "a\u2028b", "a b"},
		{"unicode paragraph separator", "a\u2029b", "a b"},
		{"tab", "a\tb", "a b"},
		{"newline survives, the preview draws with it", "a\nb", "a\nb"},
		{"empty", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Text(tt.value); got != tt.want {
				t.Errorf("Text(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

// Text is called on every field of every row on every frame, so the common case
// — a value with nothing to neutralise — must not allocate.
func TestTextReturnsACleanValueUnchanged(t *testing.T) {
	value := "eng-3160-cache-warmup"
	if got := Text(value); got != value {
		t.Errorf("Text(%q) = %q, want the input back", value, got)
	}
}

func TestArgValueRefusesWhatAParserWouldReadAsAFlag(t *testing.T) {
	for _, tt := range []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"a uuid", "0f8c1a3e-2b41-4d7a-9c6e-11f2a3b4c5d6", false},
		{"an opencode id", "ses_7Kq2mZ", false},
		{"an absolute transcript path", "/Users/x/.claude/projects/p/a.jsonl", false},
		{"a branch name", "eng-3160-cache-warmup", false},
		{"empty", "", true},
		{"a long flag", "--dangerously-skip-permissions", true},
		{"a short flag", "-v", true},
		{"a bare dash", "-", true},
		{"a flag with a value", "--json=/etc/passwd", true},
		{"an escape sequence", "id\x1b]52;c;x\x07", true},
		{"a newline", "id\nmore", true},
		{"a unicode line separator", "id\u2028more", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ArgValue(tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("ArgValue(%q) = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
			if err == nil {
				return
			}
			// An error with no reason in it leaves the picker's footer saying
			// only that something was refused.
			reason := err.Error()
			if !strings.Contains(reason, "flag") && !strings.Contains(reason, "control") && !strings.Contains(reason, "empty") {
				t.Errorf("ArgValue(%q) error %q names no reason", tt.value, reason)
			}
		})
	}
}

func TestSessionCleansEveryFieldARendererDraws(t *testing.T) {
	payload := "x\x1b]52;c;cHduZWQ=\x07"
	s := session.Session{
		Agent: payload, ID: payload, Name: payload, Title: payload,
		Cwd: payload, GitBranch: payload, Transcript: payload,
		Status: payload, JobState: payload, Detail: payload,
		PID: 42, Live: true,
	}

	got := Session(s)
	for name, field := range map[string]string{
		"Agent": got.Agent, "ID": got.ID, "Name": got.Name, "Title": got.Title,
		"Cwd": got.Cwd, "GitBranch": got.GitBranch, "Transcript": got.Transcript,
		"Status": got.Status, "JobState": got.JobState, "Detail": got.Detail,
	} {
		if strings.ContainsRune(field, 0x1b) {
			t.Errorf("Session left an escape in %s: %q", name, field)
		}
	}
	if got.PID != 42 || !got.Live {
		t.Errorf("Session disturbed a non-string field: pid=%d live=%v", got.PID, got.Live)
	}
}

func TestMessageCleansProseAndEveryContentBlock(t *testing.T) {
	payload := "x\x1b]52;c;cHduZWQ=\x07"
	m := session.Message{
		Role: "user",
		Text: payload,
		Content: []session.Content{
			{Kind: session.ContentText, Text: payload},
			{Kind: session.ContentToolCall, Tool: payload, Arg: payload},
			{Kind: session.ContentToolResult, Result: payload},
		},
	}

	got := Message(m)
	if strings.ContainsRune(got.Text, 0x1b) {
		t.Errorf("Message left an escape in Text: %q", got.Text)
	}
	for i, c := range got.Content {
		for name, field := range map[string]string{"Text": c.Text, "Tool": c.Tool, "Arg": c.Arg, "Result": c.Result} {
			if strings.ContainsRune(field, 0x1b) {
				t.Errorf("Message left an escape in Content[%d].%s: %q", i, name, field)
			}
		}
		if c.Kind != m.Content[i].Kind {
			t.Errorf("Message changed Content[%d].Kind", i)
		}
	}
}

// A nil slice must stay nil: the picker's empty checks read length, but a caller
// returning nil alongside an error should not have that turned into an empty
// non-nil slice on the way through.
func TestSessionsAndMessagesPreserveNil(t *testing.T) {
	if got := Sessions(nil); got != nil {
		t.Errorf("Sessions(nil) = %v, want nil", got)
	}
	if got := Messages(nil); got != nil {
		t.Errorf("Messages(nil) = %v, want nil", got)
	}
}
