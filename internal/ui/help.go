package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// helpBinding is one key and what it does. Two are drawn per line, which is
// what keeps the panel short enough to sit at the bottom of the screen without
// pushing the rows off it.
type helpBinding struct {
	key  string
	what string
}

// helpBindings is the list in the order it is drawn: down the left column
// first, then down the right. Every browsing-mode key appears here, because a
// help panel that omits one is worse than no panel — it says the key does not
// exist.
var helpBindings = []helpBinding{
	{"⏎", "preview the last messages"},
	{"f", "fork into a new session"},
	{"^p", "actions and commands"},
	{"^j", "focus its terminal pane"},
	{"^h", "only sessions with a process"},
	{"/", "filter name, dir, branch, ticket"},
	{"j/k ↑/↓", "move"},
	{"^d/^u", "half page"},
	{"g/G", "first, last"},
	{"? q", "close this, quit"},
}

// helpKeyWidth columns the keys so their descriptions start at the same cell.
// Computed from the bindings rather than written down, so a longer key added
// above cannot silently overlap the text beside it.
var helpKeyWidth = func() int {
	widest := 0
	for _, b := range helpBindings {
		widest = max(widest, lipgloss.Width(b.key))
	}
	return widest
}()

// helpColumns is how many binding columns the panel draws side by side.
const helpColumns = 2

// helpLines is the panel's total height: the box's two borders plus one line
// per row of bindings.
var helpLines = 2 + (len(helpBindings)+helpColumns-1)/helpColumns

// helpPanel renders the bordered key list. It always returns helpLines lines,
// which is what frameFor counted against the terminal height before this was
// called.
func (m model) helpPanel() []string {
	width := tableWidth(m.width)
	if m.width > 0 {
		width = min(width, m.width)
	}

	rows := (len(helpBindings) + helpColumns - 1) / helpColumns
	// The pair width is measured on the widest entry rather than split evenly:
	// an even split leaves the left column padded past its longest text on a
	// wide terminal, which reads as a gap rather than a column.
	pairWidth := 0
	for _, b := range helpBindings {
		pairWidth = max(pairWidth, helpKeyWidth+helpKeyGap+lipgloss.Width(b.what))
	}

	lines := make([]string, 0, helpLines)
	lines = append(lines, dimStyle.Render(boxTop("keys", width)))
	for row := range rows {
		var pairs []string
		for column := range helpColumns {
			i := column*rows + row
			if i >= len(helpBindings) {
				break
			}
			b := helpBindings[i]
			pairs = append(pairs, pad(pad(b.key, helpKeyWidth)+strings.Repeat(" ", helpKeyGap)+b.what, pairWidth))
		}
		lines = append(lines, boxRow(strings.TrimRight(strings.Join(pairs, strings.Repeat(" ", helpColumnGap)), " "), width))
	}
	return append(lines, dimStyle.Render(boxBottom(width)))
}

const (
	helpKeyGap    = 2
	helpColumnGap = 3
)
