package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	// modalMaxWidth keeps a chooser readable on a wide terminal: a list of short
	// labels stretched across 200 columns puts its notes so far from its labels
	// that the two stop reading as one row.
	modalMaxWidth = 64
	// modalMinWidth is the narrowest box worth floating. Below it the background
	// either side is a sliver rather than context, so the chooser takes the
	// whole screen instead.
	modalMinWidth = 34
	// modalMargin is how much of the frame behind stays uncovered: columns
	// either side, lines above and below.
	modalMargin = 4
	// modalBorders is the box's own two lines.
	modalBorders = 2
)

// modalItem is one line inside a chooser: a group header, the blank separating
// two groups, or a selectable entry with an optional right-aligned note. The
// headers and blanks are items rather than a rendering flourish so the window
// and the cursor keep working in units of one line, as the palette's own entry
// list already does.
type modalItem struct {
	label    string
	note     string
	header   bool
	blank    bool
	selected bool
}

// modalFrame is how wide a chooser's box draws and how many item lines fit in
// it. A zero width means the terminal has no room for a box at all, which is
// the caller's signal to draw its full-screen view instead.
type modalFrame struct {
	width int
	items int
}

// modalFrameFor sizes the box against the terminal, leaving modalMargin of the
// frame behind uncovered on every side. chrome is how many of the box's inner
// lines the caller spends on things that are not items — its search line, its
// hint, the blanks around them. count is how many items there are to draw: a box
// stretched to the terminal instead would put a field of empty rows under a list
// of two.
func modalFrameFor(width, height, chrome, count int) modalFrame {
	if width <= 0 || height <= 0 {
		return modalFrame{}
	}

	boxWidth := min(modalMaxWidth, width-2*modalMargin)
	room := height - 2*modalMargin - modalBorders - chrome
	if boxWidth < modalMinWidth || room < 1 {
		return modalFrame{}
	}
	// One line always survives, for the empty-state message a filter that matched
	// nothing stands its list down to.
	return modalFrame{width: boxWidth, items: min(room, max(count, 1))}
}

// modalBox frames content, every line exactly width cells wide so the
// compositing behind it has one rectangle to cut out.
func modalBox(title, right string, content []string, width int) []string {
	lines := make([]string, 0, len(content)+modalBorders)
	lines = append(lines, dimStyle.Render(boxTopWith(title, right, width)))
	for _, line := range content {
		lines = append(lines, boxRow(line, width))
	}
	return append(lines, dimStyle.Render(boxBottom(width)))
}

// modalPlacement centres a box of this size over a frame of that size.
func modalPlacement(boxHeight, boxWidth, width, height int) (left, top int) {
	return max((width-boxWidth)/2, 0), max((height-boxHeight)/2, 0)
}

// modalInnerWidth is how wide a line inside the box may be: boxRow spends two
// cells on each border and one on each of the spaces inside them.
func modalInnerWidth(boxWidth int) int { return max(boxWidth-4, 1) }

// modalLines renders items to exactly inner cells each. Every line is built to
// the full width rather than padded afterwards, because the selected one is a
// filled bar: padding added outside the style would leave the bar stopping short
// of the border.
func modalLines(items []modalItem, inner int) []string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		switch {
		case item.blank:
			lines = append(lines, strings.Repeat(" ", inner))
		case item.header:
			lines = append(lines, dimStyle.Render(pad(truncate(item.label, inner), inner)))
		default:
			lines = append(lines, modalEntryLine(item, inner))
		}
	}
	return lines
}

func modalEntryLine(item modalItem, inner int) string {
	marker := "  "
	if item.selected {
		marker = "▸ "
	}
	label := marker + item.label

	// The note is an annotation and the label is the thing being chosen, so a
	// row with room for only one keeps the label — and keeps a cell between the
	// two so they never read as one string.
	note := item.note
	if lipgloss.Width(label)+lipgloss.Width(note)+1 > inner {
		note = ""
	}
	label = truncate(label, inner-lipgloss.Width(note))

	line := pad(label, inner-lipgloss.Width(note))
	if item.selected {
		return selectedBarStyle.Render(line + note)
	}
	return line + dimStyle.Render(note)
}

// boxTopWith is boxTop with a key named at the right of the border, the way a
// modal says how to leave. The title is what the modal is for, so on a border
// too narrow for both it is the half that survives.
func boxTopWith(title, right string, width int) string {
	label := truncate("─ "+title+" ", max(width-2, 0))
	tag := ""
	if right != "" {
		tag = "─ " + right + " ─"
	}

	fill := width - 2 - lipgloss.Width(label) - lipgloss.Width(tag)
	if fill < 1 {
		tag = ""
		fill = width - 2 - lipgloss.Width(label)
	}
	return truncate("╭"+label+strings.Repeat("─", max(fill, 0))+tag+"╮", max(width, 0))
}

// modalSearchLine draws the query with a caret at the end, dimming the prompt so
// what was typed is the part that stands out.
func modalSearchLine(query string, inner int) string {
	const prompt = "search  "
	return pad(truncate(dimStyle.Render(prompt)+query+"_", inner), inner)
}

// modalHintLine names the keys inside the box, since a modal covers the footer
// the frame behind it draws them in.
func modalHintLine(hint string, inner int) string {
	return dimStyle.Render(pad(truncate(hint, inner), inner))
}

// modalWindow is the slice of items on screen, scrolled only as far as keeping
// the cursor inside the box requires.
func modalWindow(items []modalItem, cursor, offset, capacity int) (start, end int) {
	start = scrollWindow(cursor, offset, len(items), capacity)
	return start, min(start+capacity, len(items))
}

// modalTitle joins a modal's two-part title without leaving a dash hanging when
// the second half is empty.
func modalTitle(what, subject string) string {
	if subject == "" {
		return what
	}
	return what + " — " + subject
}

// modalOver frames content and composites it over the frame behind, centred.
func modalOver(behind string, title string, content []string, boxWidth, width, height int) string {
	box := modalBox(title, "esc", content, boxWidth)
	left, top := modalPlacement(len(box), boxWidth, width, height)
	return strings.Join(composite(strings.Split(behind, "\n"), box, left, top), "\n")
}

// modalContent assembles one chooser's inner lines: its own chrome around the
// window of items, padded so the box keeps its height as the list shortens under
// a filter. empty stands in for the items when nothing matched, since a box that
// silently loses its list reads as a chooser that failed to draw.
func modalContent(items []modalItem, cursor, offset, capacity, inner int, head []string, empty, hint string) []string {
	content := make([]string, 0, len(head)+capacity+2)
	content = append(content, head...)

	var rendered []string
	if len(items) == 0 {
		rendered = []string{dimStyle.Render(pad(truncate(empty, inner), inner))}
	} else {
		start, end := modalWindow(items, cursor, offset, capacity)
		rendered = modalLines(items[start:end], inner)
	}
	content = append(content, padTo(rendered, capacity)...)

	return append(content, "", modalHintLine(hint, inner))
}
