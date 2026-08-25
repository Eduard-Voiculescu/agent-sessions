package session

// Message is one line of conversation extracted from a transcript for the
// picker's preview.
type Message struct {
	// Role is the transcript record's own type, "user" or "assistant".
	Role string
	// Text is a Message's content when it is nothing but prose. It exists
	// so a caller with no tool calls to carry — every hand-built fixture in
	// this codebase's own tests — can construct a Message without touching
	// Content at all; Parts folds it in as a single ContentText block.
	Text string
	// Content is the message's content blocks in transcript order — prose,
	// a tool call, or that call's result, interleaved exactly as the
	// transcript recorded them. The extractor populates this instead of
	// Text whenever a message holds anything beyond plain prose.
	Content []Content
}

// Parts returns m's content blocks: Content itself if the extractor
// populated it, otherwise Text wrapped as a single prose block. Callers that
// render a Message walk this rather than choosing between the two fields
// themselves.
func (m Message) Parts() []Content {
	if len(m.Content) > 0 {
		return m.Content
	}
	if m.Text == "" {
		return nil
	}
	return []Content{{Kind: ContentText, Text: m.Text}}
}

// ContentKind selects which of Content's fields are meaningful.
type ContentKind int

const (
	// ContentText is prose: Text holds it.
	ContentText ContentKind = iota
	// ContentToolCall is a tool_use block: Tool holds the tool's name, Arg
	// the one input value that identifies what it did.
	ContentToolCall
	// ContentToolResult is a tool_result block: Result holds its first
	// non-empty line, already capped to a sane length.
	ContentToolResult
)

// Content is one block of a Message's Content slice.
type Content struct {
	Kind   ContentKind
	Text   string
	Tool   string
	Arg    string
	Result string
}
