package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

func plainLine(role, text string) string {
	return fmt.Sprintf(`{"type":%q,"sessionId":"s1","message":{"role":%q,"content":%q}}`, role, role, text)
}

func blocksLine(role string, blocks string) string {
	return fmt.Sprintf(`{"type":%q,"sessionId":"s1","message":{"role":%q,"content":[%s]}}`, role, role, blocks)
}

func TestReadPreviewExtractsMessages(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []session.Message
	}{
		{
			name: "plain string content",
			body: plainLine("user", "hello there") + "\n",
			want: []session.Message{{Role: "user", Text: "hello there"}},
		},
		{
			name: "block array content with text",
			body: blocksLine("assistant", `{"type":"text","text":"got it"}`) + "\n",
			want: []session.Message{{Role: "assistant", Text: "got it"}},
		},
		{
			name: "a bare tool_use block carries its name and no argument",
			body: blocksLine("assistant", `{"type":"tool_use","name":"Edit"}`) + "\n",
			want: []session.Message{{Role: "assistant", Content: []session.Content{
				{Kind: session.ContentToolCall, Tool: "Edit"},
			}}},
		},
		{
			name: "a bare tool_result block carries an empty result",
			body: blocksLine("user", `{"type":"tool_result","content":"ok"}`) + "\n",
			want: []session.Message{{Role: "user", Content: []session.Content{
				{Kind: session.ContentToolResult, Result: "ok"},
			}}},
		},
		{
			name: "a message with no content is skipped",
			body: strings.Join([]string{
				plainLine("user", ""),
				plainLine("user", "the real prompt"),
			}, "\n") + "\n",
			want: []session.Message{{Role: "user", Text: "the real prompt"}},
		},
		{
			name: "non-conversation types are skipped",
			body: strings.Join([]string{
				`{"type":"ai-title","aiTitle":"a title","sessionId":"s1"}`,
				plainLine("user", "kept"),
			}, "\n") + "\n",
			want: []session.Message{{Role: "user", Text: "kept"}},
		},
		{
			name: "text and tool_use in the same message interleave into Content, in order",
			body: blocksLine("assistant", `{"type":"text","text":"let me check"},{"type":"tool_use","name":"Read","input":{"file_path":"a.rb"}}`) + "\n",
			want: []session.Message{{Role: "assistant", Content: []session.Content{
				{Kind: session.ContentText, Text: "let me check"},
				{Kind: session.ContentToolCall, Tool: "Read", Arg: "a.rb"},
			}}},
		},
		{
			name: "a Bash tool_use yields its command",
			body: blocksLine("assistant", `{"type":"tool_use","name":"Bash","input":{"command":"go test ./...","description":"run tests"}}`) + "\n",
			want: []session.Message{{Role: "assistant", Content: []session.Content{
				{Kind: session.ContentToolCall, Tool: "Bash", Arg: "go test ./..."},
			}}},
		},
		{
			name: "a Read tool_use yields its file_path",
			body: blocksLine("assistant", `{"type":"tool_use","name":"Read","input":{"file_path":"/Users/dev/app/approver.rb","limit":100}}`) + "\n",
			want: []session.Message{{Role: "assistant", Content: []session.Content{
				{Kind: session.ContentToolCall, Tool: "Read", Arg: "/Users/dev/app/approver.rb"},
			}}},
		},
		{
			name: "a Skill tool_use yields its skill name",
			body: blocksLine("assistant", `{"type":"tool_use","name":"Skill","input":{"skill":"security-agent","args":"bump versions"}}`) + "\n",
			want: []session.Message{{Role: "assistant", Content: []session.Content{
				{Kind: session.ContentToolCall, Tool: "Skill", Arg: "security-agent"},
			}}},
		},
		{
			name: "a tool with a description but none of the named keys falls back to it",
			body: blocksLine("assistant", `{"type":"tool_use","name":"Agent","input":{"description":"survey the branch","prompt":"go look"}}`) + "\n",
			want: []session.Message{{Role: "assistant", Content: []session.Content{
				{Kind: session.ContentToolCall, Tool: "Agent", Arg: "survey the branch"},
			}}},
		},
		{
			name: "a tool with none of the known keys falls back to its first field",
			body: blocksLine("assistant", `{"type":"tool_use","name":"CustomTool","input":{"target":"foo.txt","note":"bar"}}`) + "\n",
			want: []session.Message{{Role: "assistant", Content: []session.Content{
				{Kind: session.ContentToolCall, Tool: "CustomTool", Arg: "foo.txt"},
			}}},
		},
		{
			name: "a tool_result whose content is a block array uses its first text block",
			body: blocksLine("user", `{"type":"tool_result","content":[{"type":"text","text":"line one\nline two"}]}`) + "\n",
			want: []session.Message{{Role: "user", Content: []session.Content{
				{Kind: session.ContentToolResult, Result: "line one"},
			}}},
		},
		{
			name: "an empty tool_result yields an empty result line",
			body: blocksLine("user", `{"type":"tool_result","content":""}`) + "\n",
			want: []session.Message{{Role: "user", Content: []session.Content{
				{Kind: session.ContentToolResult, Result: ""},
			}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTranscript(t, t.TempDir(), "s1.jsonl", tt.body)

			got, err := readPreview(path)
			if err != nil {
				t.Fatalf("readPreview() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("readPreview() = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if !reflect.DeepEqual(got[i], tt.want[i]) {
					t.Errorf("readPreview()[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// A tool_result's first line can itself run to thousands of characters (a
// minified JSON blob, say), and previewLineCap must cut it down regardless
// of where the newlines in the surrounding content fall.
func TestReadPreviewCapsAVeryLongToolResultLine(t *testing.T) {
	huge := strings.Repeat("x", 16000)
	body := blocksLine("user", fmt.Sprintf(`{"type":"tool_result","content":%q}`, huge)) + "\n"
	path := writeTranscript(t, t.TempDir(), "s1.jsonl", body)

	got, err := readPreview(path)
	if err != nil {
		t.Fatalf("readPreview() error = %v", err)
	}
	if len(got) != 1 || len(got[0].Content) != 1 {
		t.Fatalf("readPreview() = %+v, want one message with one tool_result block", got)
	}
	result := got[0].Content[0].Result
	if len(result) >= len(huge) {
		t.Fatalf("Result is %d chars, want it capped well below the original %d", len(result), len(huge))
	}
	if len(result) != previewLineCap {
		t.Errorf("Result is %d chars, want exactly previewLineCap (%d)", len(result), previewLineCap)
	}
}

// A single hook attachment line of 464 KB was measured in real transcripts,
// and bufio.Scanner's default 64 KB token limit fails on it; readPreview must
// share readLine's discipline rather than a fresh reader with that ceiling.
func TestReadPreviewHandlesLineLongerThan64KB(t *testing.T) {
	huge := fmt.Sprintf(`{"type":"attachment","sessionId":"s1","stdout":%q}`, strings.Repeat("x", 200<<10))
	body := strings.Join([]string{
		huge,
		plainLine("user", "after the giant attachment"),
	}, "\n") + "\n"
	path := writeTranscript(t, t.TempDir(), "s1.jsonl", body)

	got, err := readPreview(path)
	if err != nil {
		t.Fatalf("readPreview() error = %v", err)
	}
	want := []session.Message{{Role: "user", Text: "after the giant attachment"}}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want[0]) {
		t.Errorf("readPreview() = %+v, want %+v", got, want)
	}
}

func TestReadPreviewKeepsOnlyTheNewestMessages(t *testing.T) {
	var b strings.Builder
	for i := 0; i < previewMessageCount+10; i++ {
		b.WriteString(plainLine("user", fmt.Sprintf("message %02d", i)) + "\n")
	}
	path := writeTranscript(t, t.TempDir(), "s1.jsonl", b.String())

	got, err := readPreview(path)
	if err != nil {
		t.Fatalf("readPreview() error = %v", err)
	}
	if len(got) != previewMessageCount {
		t.Fatalf("readPreview() returned %d messages, want %d", len(got), previewMessageCount)
	}
	if got[0].Text != "message 10" {
		t.Errorf("readPreview()[0].Text = %q, want the oldest of the kept tail, %q", got[0].Text, "message 10")
	}
	if last := got[len(got)-1].Text; last != fmt.Sprintf("message %02d", previewMessageCount+9) {
		t.Errorf("readPreview() last message = %q, want the newest message in the file", last)
	}
}

// A transcript larger than previewInitialWindow forces the window-growing
// path in readPreview; the newest previewMessageCount messages must still
// come back regardless of how much padding precedes them.
func TestReadPreviewGrowsThePastTheInitialWindow(t *testing.T) {
	var b strings.Builder
	filler := fmt.Sprintf(`{"type":"assistant","sessionId":"s1","message":{"role":"assistant","content":%q}}`, strings.Repeat("f", 4<<10))
	for i := 0; i < 200; i++ {
		b.WriteString(filler + "\n")
	}
	for i := 0; i < previewMessageCount; i++ {
		b.WriteString(plainLine("user", fmt.Sprintf("recent %02d", i)) + "\n")
	}
	path := writeTranscript(t, t.TempDir(), "s1.jsonl", b.String())

	got, err := readPreview(path)
	if err != nil {
		t.Fatalf("readPreview() error = %v", err)
	}
	if len(got) != previewMessageCount {
		t.Fatalf("readPreview() returned %d messages, want %d", len(got), previewMessageCount)
	}
	for i, msg := range got {
		want := fmt.Sprintf("recent %02d", i)
		if msg.Text != want {
			t.Errorf("readPreview()[%d].Text = %q, want %q", i, msg.Text, want)
		}
	}
}

// The preview re-reads on every tick while a live session is open, so its window
// is a per-second cost rather than a one-off.
func TestPreviewStopsGrowingAtItsCap(t *testing.T) {
	if previewMaxWindow > 4<<20 {
		t.Errorf("previewMaxWindow is %d bytes, which a live session re-reads every tick", previewMaxWindow)
	}
	if previewMaxText > 64<<10 {
		t.Errorf("previewMaxText is %d bytes; one pasted file would fill the viewport and the heap", previewMaxText)
	}
}

func TestPreviewCapsOneMessagesText(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "abc.jsonl")

	line, err := json.Marshal(map[string]any{
		"type":    "assistant",
		"message": map[string]any{"content": strings.Repeat("x", previewMaxText*3)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	messages, err := readPreview(transcript)
	if err != nil {
		t.Fatalf("readPreview() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("readPreview() returned %d messages, want 1", len(messages))
	}
	if got := len(messages[0].Text); got > previewMaxText {
		t.Errorf("message text is %d bytes, want at most %d", got, previewMaxText)
	}
}

// A tool result is the other end of the same problem: it carries command output,
// which is unbounded.
func TestPreviewCapsAToolResult(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "abc.jsonl")

	line, err := json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{"content": []any{
			map[string]any{"type": "tool_result", "content": strings.Repeat("y", previewMaxText*3)},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	messages, err := readPreview(transcript)
	if err != nil {
		t.Fatalf("readPreview() error = %v", err)
	}
	for _, m := range messages {
		for _, part := range m.Parts() {
			if len(part.Result) > previewMaxText {
				t.Errorf("tool result is %d bytes, want at most %d", len(part.Result), previewMaxText)
			}
			if len(part.Text) > previewMaxText {
				t.Errorf("text block is %d bytes, want at most %d", len(part.Text), previewMaxText)
			}
		}
	}
}
