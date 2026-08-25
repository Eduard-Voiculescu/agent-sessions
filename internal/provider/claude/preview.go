package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

const (
	// previewMessageCount is how many of the newest messages Preview keeps: the
	// picker shows a terminal-style tail of the conversation, not the whole
	// thing.
	previewMessageCount = 20

	// previewInitialWindow is doubled from the tail until it holds
	// previewMessageCount messages or previewMaxWindow is reached. Unlike the
	// meta parser's fixed windows, a single message's own content (a pasted
	// diff, a tool result) can run to tens of kilobytes, so no fixed window
	// reliably holds twenty of them.
	//
	// The cap is a per-second cost rather than a one-off: refreshPreviewCmd
	// re-runs this on every tick while a live session's preview is open.
	previewInitialWindow = 256 << 10
	previewMaxWindow     = 4 << 20

	// previewMaxText is the most of one message's prose that is kept. A pasted
	// file would otherwise be held whole, twenty messages at a time, to draw a
	// viewport that shows a few dozen lines of it. Tool results have their own,
	// tighter bound in previewLineCap.
	previewMaxText = 64 << 10
)

// Preview reads the newest messages out of s's transcript, oldest first, for
// the picker to show before resuming.
func (p *Provider) Preview(s session.Session) ([]session.Message, error) {
	return readPreview(s.Transcript)
}

func readPreview(path string) ([]session.Message, error) {
	file, err := openTranscript(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stating transcript %s: %w", path, err)
	}
	size := info.Size()

	var messages []session.Message
	for window := int64(previewInitialWindow); ; window *= 2 {
		start := size - window
		if start < 0 {
			start = 0
		}
		messages = readMessagesFrom(file, start, size)
		if len(messages) >= previewMessageCount || start == 0 || window >= previewMaxWindow {
			break
		}
	}

	return tailMessages(messages, previewMessageCount), nil
}

// readMessagesFrom folds every user and assistant message out of [start,
// size) of file. start is 0 only once the window has grown to cover the
// whole file, which is the one case where the window does not begin mid-line.
func readMessagesFrom(file *os.File, start, size int64) []session.Message {
	var messages []session.Message
	collect := func(line []byte) {
		if msg, ok := parseMessage(line); ok {
			messages = append(messages, msg)
		}
	}

	section := io.NewSectionReader(file, start, size-start)
	if start == 0 {
		absorbWhole(section, size-start, collect)
	} else {
		absorbTail(section, size-start, collect)
	}
	return messages
}

func tailMessages(messages []session.Message, n int) []session.Message {
	if len(messages) <= n {
		return messages
	}
	return messages[len(messages)-n:]
}

// parseMessage extracts a preview line from one transcript record. Only user
// and assistant records carry conversation content; every other type (mode
// switches, ai-title, hook output) yields nothing.
func parseMessage(raw []byte) (session.Message, bool) {
	var line transcriptLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return session.Message{}, false
	}
	if line.Type != "user" && line.Type != "assistant" {
		return session.Message{}, false
	}
	return buildMessage(line.Type, line.Message.Content)
}

// buildMessage extracts message.content, which is either a plain string or
// an array of typed blocks. A message with no tool_use or tool_result block
// anywhere in it collapses to plain prose in Text, exactly as it always has;
// one that carries a tool call or result populates Content instead, since
// only Content can hold a tool's name, argument and result line without
// smuggling them into a pre-rendered string.
func buildMessage(role string, raw json.RawMessage) (session.Message, bool) {
	if len(raw) == 0 {
		return session.Message{}, false
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if text == "" {
			return session.Message{}, false
		}
		return session.Message{Role: role, Text: capText(text)}, true
	}

	var blocks []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Name    string          `json:"name"`
		Input   json.RawMessage `json:"input"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return session.Message{}, false
	}

	var texts []string
	var content []session.Content
	hasTool := false
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				capped := capText(b.Text)
				texts = append(texts, capped)
				content = append(content, session.Content{Kind: session.ContentText, Text: capped})
			}
		case "tool_use":
			hasTool = true
			content = append(content, session.Content{
				Kind: session.ContentToolCall,
				Tool: b.Name,
				Arg:  toolCallArg(b.Name, b.Input),
			})
		case "tool_result":
			hasTool = true
			content = append(content, session.Content{
				Kind:   session.ContentToolResult,
				Result: toolResultLine(b.Content),
			})
		}
	}
	if len(content) == 0 {
		return session.Message{}, false
	}
	if !hasTool {
		return session.Message{Role: role, Text: capText(strings.Join(texts, "\n"))}, true
	}
	return session.Message{Role: role, Content: content}, true
}

// previewLineCap bounds a single extracted line well below the ~16 KB a tool
// result can reach in real transcripts: without it, a giant single-line
// result would sit in memory and get measured by lipgloss.Width on every
// render frame for a line the viewport only ever shows one screen-width
// slice of.
const previewLineCap = 2000

// capText keeps the head of an oversized message. The head is what identifies
// it, and the renderer's own capMessage already reports the lines it held back.
// The cut is backed up to a rune boundary, since a byte index can land inside a
// multi-byte rune and leave a replacement character on the end.
func capText(value string) string {
	if len(value) <= previewMaxText {
		return value
	}
	cut := previewMaxText
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut]
}

func capLine(s string) string {
	if len(s) <= previewLineCap {
		return s
	}
	r := []rune(s)
	if len(r) <= previewLineCap {
		return s
	}
	return string(r[:previewLineCap])
}

// toolArgKeys maps a tool name to the input field that identifies what it
// did. Only tools common enough in real transcripts get a named mapping;
// everything else falls back to toolCallArg's "description" or first-field
// rule.
var toolArgKeys = map[string]string{
	"Bash":  "command",
	"Read":  "file_path",
	"Edit":  "file_path",
	"Write": "file_path",
	"Grep":  "pattern",
	"Glob":  "pattern",
	"Skill": "skill",
}

// toolCallArg picks the one input value that identifies what a tool_use did.
func toolCallArg(name string, input json.RawMessage) string {
	if key, ok := toolArgKeys[name]; ok {
		if v, ok := stringField(input, key); ok {
			return capLine(v)
		}
	}
	if v, ok := stringField(input, "description"); ok {
		return capLine(v)
	}
	if _, v, ok := firstField(input); ok {
		return capLine(v)
	}
	return ""
}

// stringField reads one field out of a JSON object, rendering it as a string
// regardless of its own JSON type.
func stringField(raw json.RawMessage, key string) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return "", false
	}
	v, ok := fields[key]
	if !ok {
		return "", false
	}
	return rawToString(v), true
}

// firstField returns a JSON object's first key and value in the order they
// appear in raw. A map[string]json.RawMessage would give them back in a
// randomized order, so "the first key" only means anything read off the
// token stream directly.
func firstField(raw json.RawMessage) (key, value string, ok bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return "", "", false
	}
	if d, isDelim := tok.(json.Delim); !isDelim || d != '{' {
		return "", "", false
	}
	keyTok, err := dec.Token()
	if err != nil {
		return "", "", false
	}
	k, isString := keyTok.(string)
	if !isString {
		return "", "", false
	}
	var v json.RawMessage
	if err := dec.Decode(&v); err != nil {
		return "", "", false
	}
	return k, rawToString(v), true
}

func rawToString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// toolResultLine reduces a tool_result's content to its first non-empty
// line, capped: the full content can run to tens of kilobytes (a file read,
// a build log), and the preview only ever shows a single-line summary of it.
func toolResultLine(content json.RawMessage) string {
	return capLine(firstNonEmptyLine(toolResultText(content)))
}

// toolResultText reads a tool_result's content, which is sometimes a plain
// string and sometimes an array of blocks (seen in real transcripts from
// MCP tool results); only the array's "text" blocks carry anything to show.
func toolResultText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}

	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return ""
	}

	var lines []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			lines = append(lines, b.Text)
		}
	}
	return strings.Join(lines, "\n")
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}
