package ui

import (
	"cmp"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
	"github.com/eduardvoiculescu/agent-sessions/internal/untrusted"
)

const (
	// agent, status and age are fixed: their content has a known ceiling
	// ("claude", "● waiting: input needed", "40m") that never benefits from
	// extra room.
	agentWidth  = 8
	statusWidth = 24
	ageWidth    = 4

	// name, branch and dir only get a floor here; layoutFor grows them to fill
	// whatever the terminal leaves over.
	nameMinWidth   = 18
	branchMinWidth = 12
	dirMinWidth    = 20

	columnGap   = 2
	gutterWidth = 2
)

var (
	headerStyle   = lipgloss.NewStyle().Bold(true)
	liveStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	// selectedBarStyle fills a modal's selected row rather than colouring its
	// text: a filled bar is found at a glance where a coloured word is read.
	selectedBarStyle = lipgloss.NewStyle().Reverse(true)
)

// columns is one frame's column widths, in row order.
type columns struct {
	agent, status, name, branch, dir, age int
}

// fixedRowOverhead is what a row spends before name, branch and dir get a say:
// the marker (as wide as the gutter furniture indents by), the three fixed
// columns, and the gap between all six columns.
func fixedRowOverhead() int {
	return gutterWidth + agentWidth + statusWidth + ageWidth + columnGap*5
}

// rowContentWidth is a row's width without its leading marker: what rule and
// gutter need to know to span exactly as far as the row does.
func rowContentWidth(c columns) int {
	return c.agent + c.status + c.name + c.branch + c.dir + c.age + columnGap*5
}

// layoutFor sizes name, branch and dir for the terminal width: dir grows
// fastest because directory paths are usually the longest strings on the row,
// branch next, name least since a session's name is normally a short
// generated title already comfortable at its minimum. width is 0 until the
// first tea.WindowSizeMsg arrives, which means "no limit known yet", so the
// three columns sit at their minimums.
func layoutFor(width int) columns {
	c := columns{agent: agentWidth, status: statusWidth, age: ageWidth,
		name: nameMinWidth, branch: branchMinWidth, dir: dirMinWidth}
	if width <= 0 {
		return c
	}

	const nameShare, branchShare, dirShare = 1, 4, 5
	const shareTotal = nameShare + branchShare + dirShare
	minTotal := nameMinWidth + branchMinWidth + dirMinWidth

	budget := width - fixedRowOverhead()
	if budget <= minTotal {
		// Too narrow even for the minimums: shrink all three together, in
		// proportion to their minimums, rather than let one of them starve
		// the other two. A row built from this must still never exceed width,
		// so each share is floored rather than rounded.
		c.name = shrinkToFit(nameMinWidth, minTotal, budget)
		c.branch = shrinkToFit(branchMinWidth, minTotal, budget)
		c.dir = shrinkToFit(dirMinWidth, minTotal, budget)
		return c
	}

	extra := budget - minTotal
	nameExtra := extra * nameShare / shareTotal
	branchExtra := extra * branchShare / shareTotal
	// dir absorbs whatever the floor division above left on the table, so the
	// three columns exactly fill the budget instead of wasting a column or two
	// of width to rounding.
	c.name += nameExtra
	c.branch += branchExtra
	c.dir += extra - nameExtra - branchExtra
	return c
}

// shrinkToFit floors rather than rounds, and floors budget<=0 to zero rather
// than to some minimum visible width: below fixedRowOverhead the fixed
// columns alone already claim the whole terminal, and a row must never grow
// past it just to keep a sliver of name, branch or dir on screen.
func shrinkToFit(min, minTotal, budget int) int {
	if budget <= 0 {
		return 0
	}
	return min * budget / minTotal
}

// truncate cuts value to width terminal cells, not runes: a session name comes
// from the first user prompt, so CJK and emoji are ordinary input and a rune
// count would leave every column right of the name misaligned and the row wider
// than the terminal.
func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = flatten(value)
	if lipgloss.Width(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	return cut(value, width-1) + "…"
}

// flatten collapses every run of whitespace into one space. It is what keeps a
// session name from breaking the whole frame: a name is often the raw first
// prompt, newlines and all, and lipgloss.Width measures the widest line rather
// than the total — so a five-line name reports 42 cells, passes the width check
// untouched, and renders as five physical lines where the frame budgeted one.
// bubbletea then drops the overflow off the top, taking the header and the
// cursor with it.
func flatten(value string) string {
	if strings.IndexFunc(value, isSplitting) < 0 {
		return value
	}
	return strings.Join(strings.Fields(value), " ")
}

// isSplitting reports the characters that would put a cell on a second line.
// Fields would also collapse ordinary double spaces, which is harmless but
// pointless work on the overwhelming majority of values.
func isSplitting(r rune) bool {
	return r == '\n' || r == '\r' || r == '\v' || r == '\f' || r == '\t'
}

// cut keeps whole graphemes, so a value cut where a double-width rune straddles
// the limit lands a cell short of it rather than a cell over. Style.Width is not
// used to pad in the same call: it word-wraps anything longer than the width
// into a second line, which in a table row is worse than a misaligned one.
func cut(value string, width int) string {
	return lipgloss.NewStyle().MaxWidth(width).Render(value)
}

func row(s session.Session, selected bool, c columns, width int) string {
	marker := "  "
	if selected {
		marker = "▸ "
	}

	badge := dimStyle.Render(pad("-", c.status))
	switch {
	case s.Live:
		// c.status fits the longest status the registry publishes, "● waiting:
		// input needed"; a longer one is cut rather than shifting every column
		// after it out of alignment.
		badge = liveStyle.Render(pad(truncate("● "+s.Status, c.status), c.status))
	case s.JobState != "":
		badge = jobStyle(s.JobState).Render(pad(truncate(jobGlyph(s.JobState)+" "+s.JobState, c.status), c.status))
	}

	name := pad(truncate(s.Name, c.name), c.name)
	if selected {
		name = selectedStyle.Render(name)
	}

	cells := []string{
		dimStyle.Render(pad(truncate(s.Agent, c.agent), c.agent)),
		badge,
		name,
		pad(truncate(cmp.Or(s.GitBranch, "-"), c.branch), c.branch),
		pad(truncate(shortenHome(s.Cwd), c.dir), c.dir),
		pad(truncate(session.Age(s.LastActive), c.age), c.age),
	}

	return clampLine(strings.TrimRight(marker+strings.Join(cells, strings.Repeat(" ", columnGap)), " "), width)
}

// jobGlyph and jobStyle mark a background agent's own state, which arrives
// from its record rather than from a running process: a job runs under the
// daemon and never appears in the live registry, so a bare dot would read as a
// live process this tool cannot actually reach — no pid means no jump, no kill
// and no send.
func jobGlyph(state string) string {
	switch state {
	case "working":
		return "◐"
	case "blocked":
		return "◼"
	default:
		return "○"
	}
}

func jobStyle(state string) lipgloss.Style {
	switch state {
	case "working":
		return liveStyle
	case "blocked":
		return errorStyle
	default:
		return dimStyle
	}
}

// thousands groups a token count so 678215 reads at a glance rather than being
// counted digit by digit.
func thousands(n int) string {
	digits := strconv.Itoa(n)
	var grouped strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteRune(d)
	}
	return grouped.String()
}

// padTo grows a content area to the height its frame reserved for it. Without
// it the furniture below floats up under a short list — filtering 156 rows down
// to four would carry the footer up with them, off the bottom of the screen
// where it belongs. Content longer than the reservation is returned untouched:
// the frame's own capacity accounting is what keeps that from happening, and
// silently cutting here would hide it.
func padTo(content []string, height int) []string {
	for len(content) < height {
		content = append(content, "")
	}
	return content
}

func columnHeaderLine(c columns, width int) string {
	cells := []string{
		pad(truncate("AGENT", c.agent), c.agent),
		pad(truncate("STATUS", c.status), c.status),
		pad(truncate("NAME", c.name), c.name),
		pad(truncate("BRANCH", c.branch), c.branch),
		pad(truncate("DIRECTORY", c.dir), c.dir),
		pad(truncate("AGE", c.age), c.age),
	}
	line := strings.Repeat(" ", gutterWidth) + strings.Join(cells, strings.Repeat(" ", columnGap))
	return dimStyle.Render(clampLine(strings.TrimRight(line, " "), width))
}

// clampLine stops a built line at the terminal edge. The gutter, the three fixed
// columns and the five gaps between all six claim 48 cells before name, branch
// and dir get a say, so under that width layoutFor has already collapsed
// everything it can and only the finished line can be cut; a line past the edge
// wraps, and every wrapped row costs the frame a line it never reserved.
func clampLine(line string, width int) string {
	if width <= 0 {
		return line
	}
	return truncate(line, width)
}

// boundary is the index of the first history row that follows a live one, or -1
// when the rows are all of one kind and no divider belongs anywhere.
func boundary(rows []session.Session) int {
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Live && !rows[i].Live {
			return i
		}
	}
	return -1
}

// rule spans exactly as far as the table's rows do, not the full terminal
// width the row's columns were free to fill: below the width where layoutFor
// has to shrink name, branch and dir to fit, the two diverge, and a rule wider
// than every row it separates reads as a frame that disagrees with itself.
func rule(width int) string {
	total := tableWidth(width)
	if width > 0 {
		total = min(total, width)
	}
	return dimStyle.Render(gutter(strings.Repeat("─", max(total-gutterWidth, 0)), width))
}

// errorLine flattens the message: errors.Join separates its causes with newlines
// and one embedded newline would push the footer out of the frame.
func errorLine(err error, width int) string {
	return errorStyle.Render(gutter(strings.Join(strings.Fields("error: "+untrusted.Text(err.Error())), " "), width))
}

// gutter indents furniture to where the rows start and stops it at the window
// edge, so a line too long for the terminal cannot wrap and cost a second row.
// The indent itself is clamped too: at a width below it there is no room for
// both.
func gutter(text string, width int) string {
	return clampLine(strings.Repeat(" ", gutterWidth)+truncate(text, span(width)), width)
}

// span is how far full-width furniture may draw. Columns are sized to fill the
// terminal now, so a row's content is exactly width-gutterWidth wide once a
// real width is known; before that (width is 0 until the first
// tea.WindowSizeMsg), furniture spans what a minimum-width row would.
func span(width int) int {
	if width <= 0 {
		return rowContentWidth(layoutFor(0))
	}
	return max(width-gutterWidth, 1)
}

// tableWidth is how wide a table row draws, marker included, at this terminal
// width. The header block aligns to this so its frame and the table's rows
// read as one unit instead of disagreeing on where the table ends.
func tableWidth(width int) int {
	return gutterWidth + rowContentWidth(layoutFor(width))
}

func pad(v string, width int) string {
	gap := width - lipgloss.Width(v)
	if gap <= 0 {
		return v
	}
	return v + strings.Repeat(" ", gap)
}

func shortenHome(dir string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(dir, home) {
		return dir
	}
	return filepath.Join("~", strings.TrimPrefix(dir, home))
}

// boxTop, boxRow and boxBottom draw the header block's rounded frame, each line
// truncated to width: below the five cells a bordered line needs — two corners,
// two spaces and one cell of content — the furniture alone would overrun the
// terminal, so the line is cut rather than drawn whole.
func boxTop(title string, width int) string {
	label := truncate("─ "+title+" ", max(width-2, 0))
	fill := max(width-2-lipgloss.Width(label), 0)
	return truncate("╭"+label+strings.Repeat("─", fill)+"╮", max(width, 0))
}

func boxBottom(width int) string {
	return truncate("╰"+strings.Repeat("─", max(width-2, 0))+"╯", max(width, 0))
}

func boxRow(content string, width int) string {
	inner := max(width-4, 0)
	return truncate("│ "+pad(truncate(content, inner), inner)+" │", max(width, 0))
}
