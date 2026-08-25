package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

// width is a count of terminal cells, not of runes: a session name comes from
// the first user prompt, so CJK and emoji are ordinary input, and a value that a
// rune count calls short can still be twice as wide as the column holding it.
func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		value string
		width int
		want  string
	}{
		{name: "short value is untouched", value: "abc", width: 10, want: "abc"},
		{name: "exact fit is untouched", value: "abcde", width: 5, want: "abcde"},
		{name: "long value gets an ellipsis", value: "abcdefgh", width: 5, want: "abcd…"},
		{name: "width of one yields the ellipsis", value: "abcdefgh", width: 1, want: "…"},
		{name: "zero width yields empty", value: "abc", width: 0, want: ""},
		{name: "combining marks cost no cell", value: "héllo wörld", width: 6, want: "héllo…"},
		{name: "a double-width value inside the cell width is untouched", value: "日本", width: 4, want: "日本"},
		{name: "three runes are six cells, not three", value: "日本語", width: 4, want: "日…"},
		{name: "a long double-width value is cut in cells", value: "日本語のセッション名です", width: 6, want: "日本…"},
		{name: "emoji are cut in cells", value: "🚀🚀🚀 fix", width: 5, want: "🚀🚀…"},
		{name: "a cut inside a double-width rune lands short, never over", value: "日本語", width: 5, want: "日本…"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.value, tt.width)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.value, tt.width, got, tt.want)
			}
			if cells := lipgloss.Width(got); cells > tt.width {
				t.Errorf("truncate(%q, %d) = %q, %d cells wide", tt.value, tt.width, got, cells)
			}
		})
	}
}

func TestPadFillsToTheCellWidth(t *testing.T) {
	tests := []struct {
		name  string
		value string
		width int
		want  int
	}{
		{name: "ascii", value: "abc", width: 6, want: 6},
		{name: "double-width runes pad by cells", value: "日本", width: 6, want: 6},
		{name: "emoji pad by cells", value: "🚀", width: 4, want: 4},
		{name: "a value already past the width is left alone", value: "日本語", width: 4, want: 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lipgloss.Width(pad(tt.value, tt.width)); got != tt.want {
				t.Errorf("pad(%q, %d) is %d cells wide, want %d", tt.value, tt.width, got, tt.want)
			}
		})
	}
}

func TestRowRendersTheAgent(t *testing.T) {
	line := row(session.Session{Agent: "codex", Name: "a thread", LastActive: time.Now()}, false, layoutFor(0), 0)

	if !strings.Contains(line, "codex") {
		t.Errorf("row() = %q, want it to name the agent", line)
	}
}

// A divider narrower than the row it separates visibly falls short of the last
// column, and one wider than the row overshoots the table's frame; either way
// the rule is derived from the same widths the row is built from. This must
// hold at every terminal width, not just the one the columns were designed
// around, since name, branch and dir now resize with the terminal, and for
// double-width and emoji content as much as for ASCII: a row measured in runes
// overflows the rule by one cell per double-width rune on it. Below the width
// where layoutFor has to shrink every column's share to stay inside the
// budget, the rule used to fall back to the full terminal width rather than
// the row's actual, shrunken width. The rule is pinned to the table's own
// width, and the row only bounded by it: a row ends in TrimRight, so any age
// shorter than the four cells the column reserves ("now", "5h") leaves it a
// cell or two short of the rule by design.
func TestRuleSpansEveryColumnAtEveryWidth(t *testing.T) {
	fills := []struct {
		name string
		text func(cells int) string
	}{
		{name: "ascii", text: func(cells int) string { return strings.Repeat("n", cells) }},
		{name: "cjk", text: func(cells int) string { return strings.Repeat("日", cells) }},
		{name: "emoji", text: func(cells int) string { return strings.Repeat("🚀", cells) }},
	}

	for _, fill := range fills {
		for _, width := range []int{0, 1, 5, 20, 40, 47, 48, 60, 80, 97, 98, 120, 150, 200} {
			t.Run(fmt.Sprintf("%s/%d", fill.name, width), func(t *testing.T) {
				cols := layoutFor(width)
				full := session.Session{
					Agent:      "opencode",
					Name:       fill.text(cols.name + 10),
					GitBranch:  fill.text(cols.branch + 10),
					Cwd:        "/" + fill.text(cols.dir+10),
					Live:       true,
					Status:     "waiting: input needed",
					LastActive: time.Now().Add(-100 * 24 * time.Hour),
				}

				spans := tableWidth(width)
				if width > 0 {
					spans = min(spans, width)
				}
				if got := lipgloss.Width(rule(width)); got != spans {
					t.Errorf("width=%d: the rule spans %d columns, want the table's %d", width, got, spans)
				}
				if got := lipgloss.Width(row(full, false, cols, width)); got > spans {
					t.Errorf("width=%d: row() is %d columns wide, past the rule's %d", width, got, spans)
				}
			})
		}
	}
}

// The name is the one column filled from arbitrary user text, so it is where a
// rune-counted layout breaks: the row grows a cell per double-width rune, every
// column after it slides out from under its header label, and AGE is pushed off
// the terminal.
func TestRowWidthAndAlignmentSurviveWideNames(t *testing.T) {
	names := []struct{ label, name string }{
		{label: "cjk", name: "日本語のセッション名です"},
		{label: "emoji", name: "🚀🚀🚀 fix the thing 🚀🚀🚀"},
		{label: "mixed", name: "日本語 🚀 fix the thing"},
		{label: "ascii", name: "fix the thing"},
	}

	for _, tt := range names {
		for _, width := range []int{48, 80, 120, 150} {
			t.Run(fmt.Sprintf("%s/%d", tt.label, width), func(t *testing.T) {
				cols := layoutFor(width)
				s := session.Session{
					Agent:      "claude",
					Name:       tt.name,
					GitBranch:  "eng-3110",
					Cwd:        "/Users/dev/git/webapp",
					Live:       true,
					Status:     "waiting: input needed",
					LastActive: time.Now().Add(-100 * 24 * time.Hour),
				}

				line := row(s, false, cols, width)
				got, want := lipgloss.Width(line), gutterWidth+rowContentWidth(cols)
				if got != want {
					t.Errorf("row() is %d cells wide, want the layout's %d", got, want)
				}
				if got > width {
					t.Errorf("row() is %d cells wide, wider than the %d-column terminal", got, width)
				}

				// The age cell is the one past every flexible column, so its offset
				// is where a name mismeasured by even one cell shows up.
				header := columnHeaderLine(cols, width)
				if ageAt, labelAt := cellOffset(line, "100d"), cellOffset(header, "AGE"); ageAt != labelAt {
					t.Errorf("age cell starts at cell %d but its AGE label at %d:\n%s\n%s", ageAt, labelAt, header, line)
				}
			})
		}
	}
}

// cellOffset is how many terminal cells precede needle on line, which is what
// "these two lines align" means once a cell and a rune stop being the same
// thing. It takes the last occurrence, because AGE is also the first three
// letters of AGENT.
func cellOffset(line, needle string) int {
	i := strings.LastIndex(line, needle)
	if i < 0 {
		return -1
	}
	return lipgloss.Width(line[:i])
}

func TestSpanStopsAtTheTerminalEdge(t *testing.T) {
	tests := []struct {
		name  string
		width int
		want  int
	}{
		{name: "unknown width spans a minimum-width row", width: 0, want: rowContentWidth(layoutFor(0))},
		// Columns fill the terminal now, so a wide terminal must not be capped
		// at some historical fixed total: the rule has to keep pace with the
		// row, which keeps pace with the terminal.
		{name: "wide terminal spans further than a narrow one", width: 300, want: 300 - gutterWidth},
		{name: "narrow terminal stops at the edge", width: 40, want: 40 - gutterWidth},
		{name: "terminal narrower than the gutter still draws", width: 1, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := span(tt.width); got != tt.want {
				t.Errorf("span(%d) = %d, want %d", tt.width, got, tt.want)
			}
		})
	}
}

// rule used to span the full terminal width regardless of how much of it the
// table actually used, so below the width where columns must shrink to fit,
// the divider visibly overshot the box and the rows it separates. This pins
// the reported case: at 80 columns the table is 78 wide, not 80.
func TestRuleMatchesTheTableWidthNotTheTerminalWidth(t *testing.T) {
	if got, want := lipgloss.Width(rule(80)), tableWidth(80); got != want {
		t.Errorf("rule(80) is %d cells wide, want it to match the table's %d", got, want)
	}
	if tableWidth(80) == 80 {
		t.Fatal("tableWidth(80) == 80, this test no longer exercises the width where they diverge")
	}
}

// layoutFor is the foundation the responsive redesign rests on: agent, status
// and age never move, and name, branch and dir absorb the terminal's slack
// without ever pushing a row past the terminal's edge. Below
// fixedRowOverhead the fixed columns alone already claim the whole terminal,
// so that floor — not zero — is where "never exceeds" starts being possible
// at all; the brief asks for agent, status and age to stay fixed, which rules
// out shrinking them too.
func TestLayoutForNeverExceedsTheTerminalWidth(t *testing.T) {
	for _, width := range []int{fixedRowOverhead(), 60, 80, 97, 98, 100, 120, 150, 200, 400} {
		cols := layoutFor(width)

		if cols.agent != agentWidth || cols.status != statusWidth || cols.age != ageWidth {
			t.Errorf("width=%d: fixed columns changed: %+v", width, cols)
		}

		total := gutterWidth + rowContentWidth(cols)
		if total > width {
			t.Errorf("width=%d: row totals %d columns, wider than the terminal", width, total)
		}
	}
}

// A terminal narrower than the fixed columns' own footprint cannot show name,
// branch or dir at all without growing past the terminal edge; layoutFor
// collapses them to nothing rather than let the row overflow.
func TestLayoutForCollapsesFlexibleColumnsBelowTheFixedFootprint(t *testing.T) {
	cols := layoutFor(fixedRowOverhead() - 1)

	if cols.name != 0 || cols.branch != 0 || cols.dir != 0 {
		t.Errorf("expected name, branch and dir to collapse to nothing: %+v", cols)
	}
}

// The owner's complaint was that branch and dir stayed cramped while the
// terminal grew; the fix must actually reach them, not just name.
func TestLayoutForGrowsBranchAndDirWithTheTerminal(t *testing.T) {
	at120 := layoutFor(120)
	at150 := layoutFor(150)
	at200 := layoutFor(200)

	if !(at120.branch < at150.branch && at150.branch < at200.branch) {
		t.Errorf("branch did not grow with width: 120=%d 150=%d 200=%d", at120.branch, at150.branch, at200.branch)
	}
	if !(at120.dir < at150.dir && at150.dir < at200.dir) {
		t.Errorf("dir did not grow with width: 120=%d 150=%d 200=%d", at120.dir, at150.dir, at200.dir)
	}
	if at120.dir <= at120.branch || at200.dir <= at200.branch {
		t.Errorf("dir should stay ahead of branch, since directory paths run longer: at120=%+v at200=%+v", at120, at200)
	}
}

// A terminal too narrow even for the minimums must shrink all three flexible
// columns together, in proportion to their minimums, rather than let one win
// the row at the other two's expense.
func TestLayoutForShrinksTogetherWhenTooNarrowForTheMinimums(t *testing.T) {
	cols := layoutFor(70)

	if cols.name == 0 || cols.branch == 0 || cols.dir == 0 {
		t.Errorf("a flexible column collapsed to nothing at a width with room to spare: %+v", cols)
	}
	if cols.name >= nameMinWidth || cols.branch >= branchMinWidth || cols.dir >= dirMinWidth {
		t.Errorf("expected every flexible column below its usual minimum: %+v", cols)
	}
	if gutterWidth+rowContentWidth(cols) > 70 {
		t.Errorf("row still overflows a 70-column terminal: %+v", cols)
	}
}

// The column header is built from the same layout as the rows below it, so it
// must never draw wider than the terminal, or than the row content it names.
func TestColumnHeaderLineFitsTheLayout(t *testing.T) {
	for _, width := range []int{48, 60, 80, 120, 150, 200} {
		cols := layoutFor(width)

		headerWidth := lipgloss.Width(columnHeaderLine(cols, width))
		rowCeiling := gutterWidth + rowContentWidth(cols)
		if headerWidth > width {
			t.Errorf("width=%d: column header is %d columns wide, wider than the terminal", width, headerWidth)
		}
		if headerWidth > rowCeiling {
			t.Errorf("width=%d: column header (%d) is wider than the row layout allows (%d)", width, headerWidth, rowCeiling)
		}
	}
}

// The gutter, the three fixed columns and the five gaps between all six claim
// 48 cells before name, branch and dir get a say, so under that width
// layoutFor has already collapsed everything it can and the line itself has to
// be cut. A vertical split of an 80-column window is ~40 columns, where every
// row wrapped to two lines, doubling the frame's height until bubbletea dropped
// the header and the cursor row off the top.
func TestRowAndColumnHeaderFitEveryWidthIncludingBelowTheFixedFootprint(t *testing.T) {
	s := session.Session{
		Agent:      "claude",
		Name:       "日本語 🚀 fix the thing",
		GitBranch:  "feature/eng-3120-quick-actions",
		Cwd:        "/Users/dev/git/webapp",
		Live:       true,
		Status:     "waiting: input needed",
		LastActive: time.Now().Add(-100 * 24 * time.Hour),
	}

	for _, width := range []int{1, 2, 3, 5, 20, 39, 40, 46, 47, 48, 49, 80} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			cols := layoutFor(width)

			for _, selected := range []bool{false, true} {
				if got := lipgloss.Width(row(s, selected, cols, width)); got > width {
					t.Errorf("row(selected=%v) is %d cells wide, wider than the %d-column terminal", selected, got, width)
				}
			}
			if got := lipgloss.Width(columnHeaderLine(cols, width)); got > width {
				t.Errorf("columnHeaderLine() is %d cells wide, wider than the %d-column terminal", got, width)
			}
			if got := lipgloss.Width(rule(width)); got > width {
				t.Errorf("rule() is %d cells wide, wider than the %d-column terminal", got, width)
			}
			if got := lipgloss.Width(gutter("no sessions matched", width)); got > width {
				t.Errorf("gutter() is %d cells wide, wider than the %d-column terminal", got, width)
			}
		})
	}
}

// A bordered line spends four cells on furniture before any content, so under
// five columns boxRow has to be cut like boxTop and boxBottom already are.
func TestBoxLinesFitEveryWidth(t *testing.T) {
	for width := 1; width <= 12; width++ {
		lines := map[string]string{
			"boxTop":    boxTop("agent-sessions", width),
			"boxRow":    boxRow("sessions 42    ○ history 41", width),
			"boxBottom": boxBottom(width),
		}
		for name, line := range lines {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("%s(%d) is %d cells wide: %q", name, width, got, line)
			}
		}
	}
}

// A background agent runs under the daemon and never appears in the live
// registry, so without its own record its row shows "-" while it is working.
func TestRowShowsABackgroundAgentsOwnState(t *testing.T) {
	for _, tt := range []struct {
		state string
		want  string
	}{
		{"working", "working"},
		{"blocked", "blocked"},
		{"done", "done"},
	} {
		t.Run(tt.state, func(t *testing.T) {
			s := session.Session{Agent: "claude", Name: "job row", JobState: tt.state, Detail: "Verifying", LastActive: time.Now()}
			got := row(s, false, layoutFor(120), 120)
			if !strings.Contains(got, tt.want) {
				t.Errorf("row() does not show the job state %q:\n%s", tt.state, got)
			}
			if strings.Contains(got, "● "+tt.want) {
				t.Errorf("row() marked a job with the live dot, which promises a pid this tool has no way to reach:\n%s", got)
			}
		})
	}
}

// A live process outranks a job record: the registry knows what the process is
// doing right now, while the record is whatever the agent last wrote down.
func TestRowPrefersTheLiveStatusOverTheJobState(t *testing.T) {
	s := session.Session{
		Agent: "claude", Name: "both", Live: true, PID: 7, Status: "busy",
		JobState: "done", LastActive: time.Now(),
	}
	got := row(s, false, layoutFor(120), 120)

	if !strings.Contains(got, "● busy") {
		t.Errorf("row() does not show the live status:\n%s", got)
	}
	if strings.Contains(got, "done") {
		t.Errorf("row() showed the stale job state alongside a live process:\n%s", got)
	}
}

func TestThousandsGroupsTokenCounts(t *testing.T) {
	for _, tt := range []struct {
		n    int
		want string
	}{
		{0, "0"},
		{7, "7"},
		{999, "999"},
		{1000, "1,000"},
		{43223, "43,223"},
		{678215, "678,215"},
		{1000000, "1,000,000"},
	} {
		if got := thousands(tt.n); got != tt.want {
			t.Errorf("thousands(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// A session name is often the raw first prompt, newlines and all. lipgloss.Width
// measures the widest line rather than the total, so a multi-line name passes
// the width check untouched and renders as many physical lines where the frame
// budgeted one — which is how the whole screen came apart on scrolling onto one.
func TestRowIsOneLineEvenWhenTheNameIsAWholePastedBrief(t *testing.T) {
	name := strings.Join([]string{
		"Apply security upgrades in this repository",
		"",
		"Rules:",
		"- Edit files only. Do not commit.",
		"- If an upgrade is not possible, leave it.",
		"\tYour final message must be a single JSON object",
	}, "\n")

	s := session.Session{Agent: "claude", Name: name, Cwd: "/Users/dev/x", GitBranch: "b", LastActive: time.Now()}
	for _, width := range []int{40, 80, 120, 200} {
		got := row(s, false, layoutFor(width), width)
		if lines := strings.Count(got, "\n"); lines != 0 {
			t.Errorf("width=%d: row() rendered %d extra lines:\n%s", width, lines, got)
		}
		if lipgloss.Width(got) > width {
			t.Errorf("width=%d: row() is %d cells wide", width, lipgloss.Width(got))
		}
	}
}

// The preview header and the palette title both carry a session name straight
// into a bordered box.
func TestBoxesAreOneLineEvenWithAMultiLineTitle(t *testing.T) {
	multi := "line one\nline two\nline three"

	if got := boxTop(multi, 80); strings.Count(got, "\n") != 0 {
		t.Errorf("boxTop() rendered extra lines:\n%s", got)
	}
	if got := boxRow(multi, 80); strings.Count(got, "\n") != 0 {
		t.Errorf("boxRow() rendered extra lines:\n%s", got)
	}
}

func TestFlattenCollapsesEveryKindOfBreak(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
		want  string
	}{
		{"plain text is untouched", "already one line", "already one line"},
		{"newline", "a\nb", "a b"},
		{"carriage return", "a\rb", "a b"},
		{"tab", "a\tb", "a b"},
		{"vertical tab", "a\vb", "a b"},
		{"form feed", "a\fb", "a b"},
		{"blank line", "a\n\n\nb", "a b"},
		{"crlf", "a\r\nb", "a b"},
		{"leading and trailing", "\n  a\n", "a"},
		{"only breaks", "\n\t\n", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := flatten(tt.value); got != tt.want {
				t.Errorf("flatten(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

// firstForeignEscape returns the first escape sequence in frame that this picker
// did not put there. Every sequence it emits comes from lipgloss and is an SGR
// colour change: ESC [ digits-and-semicolons m. Anything else — an OSC, a cursor
// move, a bare ESC — arrived in the data.
func firstForeignEscape(frame string) (string, bool) {
	for i := 0; i < len(frame); i++ {
		if frame[i] != 0x1b {
			continue
		}
		rest := frame[i+1:]
		if !strings.HasPrefix(rest, "[") {
			return frame[i:min(i+16, len(frame))], true
		}
		j := 1
		for j < len(rest) && (rest[j] == ';' || (rest[j] >= '0' && rest[j] <= '9')) {
			j++
		}
		if j >= len(rest) || rest[j] != 'm' {
			return frame[i:min(i+16, len(frame))], true
		}
	}
	return "", false
}

// The picker draws values written by agents that read the internet and untrusted
// repositories. An escape sequence reaching the terminal from one of them
// rewrites the clipboard, redraws the confirmation prompt, or answers a query
// with keystrokes.
func TestNoFrameCarriesAnEscapeSequenceFromTheData(t *testing.T) {
	payload := "poc\x1b]52;c;cHduZWQ=\x07\x1b[2J\x1b7H"

	cfg := Config{
		Sessions: []session.Session{{
			Agent: "claude", ID: payload, Name: payload, Title: payload,
			Cwd: payload, GitBranch: payload, Transcript: payload,
			Status: payload, JobState: payload, Detail: payload,
			Live: true, PID: 4242, LastActive: time.Now(),
		}},
		Filter: func(s []session.Session) []session.Session { return s },
		Preview: func(session.Session) ([]session.Message, error) {
			return []session.Message{{
				Role: "assistant",
				Content: []session.Content{
					{Kind: session.ContentText, Text: payload},
					{Kind: session.ContentToolCall, Tool: payload, Arg: payload},
					{Kind: session.ContentToolResult, Result: payload},
				},
			}}, nil
		},
		LoadErr:  fmt.Errorf("could not read %s", payload),
		LiveOnly: func() bool { return false },
		Cwd:      func() string { return "" },
	}

	m := newModel(cfg)
	m.width, m.height = 120, 40

	if got, found := firstForeignEscape(m.View()); found {
		t.Errorf("the list frame carries %q from the data", got)
	}

	m.status = payload
	m.confirming, m.confirmPrompt = true, payload
	if got, found := firstForeignEscape(m.View()); found {
		t.Errorf("the confirmation frame carries %q from the data", got)
	}

	m.confirming, m.confirmPrompt = false, ""
	m.filtering, m.query = true, payload
	if got, found := firstForeignEscape(m.View()); found {
		t.Errorf("the filter frame carries %q from the data", got)
	}

	m.filtering, m.query = false, ""
	previewed, _ := m.openPreview(m.visible())
	pm, ok := previewed.(model)
	if !ok {
		t.Fatal("openPreview did not return a model")
	}
	messages, err := pm.preview(pm.previewTarget)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	pm = pm.handlePreviewResult(previewResultMsg{gen: pm.previewGen, messages: messages})
	pm.status = payload
	if got, found := firstForeignEscape(pm.View()); found {
		t.Errorf("the preview frame carries %q from the data", got)
	}
}
