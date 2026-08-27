package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// composite draws box over background with its top-left corner at column left
// and line top, and returns exactly as many lines as it was given: a modal
// floats over the frame rather than replacing it, so the rows it does not cover
// stay legible behind it.
//
// The slicing is escape-aware. Every line behind a modal is already styled, and
// cutting one with a plain string index would land inside an escape sequence and
// hand the terminal its remainder as text — a frame that loses its colours and
// its shape at once.
func composite(background []string, box []string, left, top int) []string {
	if len(box) == 0 {
		return background
	}

	width := 0
	for _, line := range box {
		width = max(width, ansi.StringWidth(line))
	}

	out := make([]string, len(background))
	copy(out, background)

	for i, line := range box {
		y := top + i
		if y < 0 || y >= len(out) {
			continue
		}
		framed := pad(line, width)
		out[y] = head(out[y], left) + framed + tail(out[y], left+width, framed)
	}
	return out
}

// head is the part of a line left of the box, padded out when the line stops
// short of it. A line cut mid-style is closed rather than left open: the style
// would otherwise bleed into the box drawn immediately after it.
func head(line string, left int) string {
	if left <= 0 {
		return ""
	}

	cut := ansi.Truncate(line, left, "")
	if strings.Contains(cut, "\x1b") {
		cut += "\x1b[0m"
	}
	return cut + strings.Repeat(" ", max(left-ansi.StringWidth(cut), 0))
}

// tail is the part of a line right of the box. A styled box is closed before it
// — the tail's own styling begins inside the part that was cut away, so without
// the reset it wears whatever the box's last cell did.
func tail(line string, from int, box string) string {
	rest := ansi.TruncateLeft(line, from, "")
	if rest == "" || !strings.Contains(box, "\x1b") {
		return rest
	}
	return "\x1b[0m" + rest
}
