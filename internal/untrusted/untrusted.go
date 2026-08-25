// Package untrusted neutralises the values this tool reads out of an agent's own
// files. A transcript is written by a process that reads the internet and
// untrusted repositories, and any process running as the user can plant one, so
// nothing taken from one may reach a terminal or another program's argv as
// itself. SECURITY.md states the boundary this package implements.
package untrusted

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

// Text replaces every character a terminal would read as a command with a
// space. ESC introduces every sequence a terminal acts on: OSC 52 rewrites the
// clipboard, so one transcript line can decide what the owner's next paste into
// a shell runs, and a cursor sequence can redraw the [y/N] prompt as a different
// question. The C1 range is included because a terminal in 8-bit mode reads
// U+009B as CSI directly, and the two Unicode line separators because they break
// a line without being unicode.IsControl.
//
// Replaced rather than dropped: dropping glues the words either side of the
// sequence together, which reads as a name the session does not have.
//
// A newline is spared. The preview splits content on it deliberately, and every
// single-line site downstream already collapses it through flatten.
func Text(value string) string {
	if strings.IndexFunc(value, unsafeInLine) < 0 {
		return value
	}
	return strings.Map(func(r rune) rune {
		if unsafeInLine(r) {
			return ' '
		}
		return r
	}, value)
}

func unsafeInLine(r rune) bool {
	if r == '\n' {
		return false
	}
	return unsafeInArg(r)
}

func unsafeInArg(r rune) bool {
	return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
}

// ArgValue reports why value cannot be handed to another program as a positional
// argument or as a flag's value, or nil when it can. The leading dash is the
// whole of the danger: `claude --resume <id>` with an id of
// --dangerously-skip-permissions is that flag rather than that id, and the id is
// only ever a transcript's filename or a field out of its JSON.
func ArgValue(value string) error {
	switch {
	case value == "":
		return errors.New("the value is empty")
	case strings.HasPrefix(value, "-"):
		return fmt.Errorf("%q begins with a dash, which the command receiving it would read as a flag of its own", value)
	}
	if i := strings.IndexFunc(value, unsafeInArg); i >= 0 {
		return fmt.Errorf("%q has a control character at byte %d", value, i)
	}
	return nil
}

// Session returns s with every string passed through Text. Cwd, ID and
// Transcript are included even though they are operated on rather than only
// drawn: a control character in any of the three is not a path or an id anyone
// meant, and cleaning them here means the value the owner read on screen is the
// value that gets acted on.
func Session(s session.Session) session.Session {
	s.Agent = Text(s.Agent)
	s.ID = Text(s.ID)
	s.Name = Text(s.Name)
	s.Title = Text(s.Title)
	s.Cwd = Text(s.Cwd)
	s.GitBranch = Text(s.GitBranch)
	s.Transcript = Text(s.Transcript)
	s.Status = Text(s.Status)
	s.JobState = Text(s.JobState)
	s.Detail = Text(s.Detail)
	return s
}

func Sessions(in []session.Session) []session.Session {
	if in == nil {
		return nil
	}
	out := make([]session.Session, len(in))
	for i, s := range in {
		out[i] = Session(s)
	}
	return out
}

func Message(m session.Message) session.Message {
	m.Role = Text(m.Role)
	m.Text = Text(m.Text)
	if len(m.Content) == 0 {
		return m
	}

	content := make([]session.Content, len(m.Content))
	for i, c := range m.Content {
		c.Text, c.Tool, c.Arg, c.Result = Text(c.Text), Text(c.Tool), Text(c.Arg), Text(c.Result)
		content[i] = c
	}
	m.Content = content
	return m
}

func Messages(in []session.Message) []session.Message {
	if in == nil {
		return nil
	}
	out := make([]session.Message, len(in))
	for i, m := range in {
		out[i] = Message(m)
	}
	return out
}
