package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func background(lines, width int) []string {
	out := make([]string, lines)
	for i := range out {
		out[i] = strings.Repeat("a", width)
	}
	return out
}

func TestCompositeKeepsTheBackgroundEitherSideOfTheBox(t *testing.T) {
	got := composite(background(3, 10), []string{"XXXX"}, 3, 1)

	if len(got) != 3 {
		t.Fatalf("composite() returned %d lines, want the background's own 3", len(got))
	}
	if got[1] != "aaaXXXXaaa" {
		t.Errorf("composite()[1] = %q, want %q", got[1], "aaaXXXXaaa")
	}
	if got[0] != strings.Repeat("a", 10) || got[2] != strings.Repeat("a", 10) {
		t.Errorf("composite() touched a line the box does not cover: %q, %q", got[0], got[2])
	}
}

// The rows behind a modal are already styled, and a naive string slice would
// cut an escape sequence in half — the terminal then reads the remainder as
// text and the frame loses its shape.
func TestCompositeLeavesAStyledBackgroundLineTheWidthItWas(t *testing.T) {
	// The escapes are written out rather than rendered through lipgloss, which
	// emits none with no TTY — and escape-aware slicing is the whole point here.
	styled := "\x1b[36m" + strings.Repeat("a", 20) + "\x1b[0m"

	got := composite([]string{styled}, []string{"XXXX"}, 8, 0)

	if width := lipgloss.Width(got[0]); width != 20 {
		t.Errorf("line is %d cells wide, want the 20 it was:\n%q", width, got[0])
	}
	if !strings.Contains(got[0], "XXXX") {
		t.Errorf("the box is missing from the line:\n%q", got[0])
	}
}

func TestCompositePadsABackgroundLineTooShortToReachTheBox(t *testing.T) {
	got := composite([]string{"aa"}, []string{"XX"}, 5, 0)

	if got[0] != "aa   XX" {
		t.Errorf("composite()[0] = %q, want the gap padded out to the box", got[0])
	}
}

func TestCompositeAlignsBoxLinesOfDifferentWidths(t *testing.T) {
	got := composite(background(2, 12), []string{"XXXX", "YY"}, 4, 0)

	if got[0] != "aaaaXXXXaaaa" {
		t.Errorf("composite()[0] = %q", got[0])
	}
	// The short line is padded to the widest, so the background cannot show
	// through the middle of the box.
	if got[1] != "aaaaYY  aaaa" {
		t.Errorf("composite()[1] = %q, want the short box line padded to the box width", got[1])
	}
}

func TestCompositeDropsBoxLinesPastTheBackground(t *testing.T) {
	got := composite(background(2, 8), []string{"XX", "XX", "XX"}, 1, 1)

	if len(got) != 2 {
		t.Fatalf("composite() returned %d lines, want the background's own 2", len(got))
	}
	if got[1] != "aXXaaaaa" {
		t.Errorf("composite()[1] = %q", got[1])
	}
}

func TestCompositeLeavesTheBackgroundAloneWhenTheBoxIsEmpty(t *testing.T) {
	lines := background(2, 6)

	got := composite(lines, nil, 2, 0)

	if got[0] != lines[0] || got[1] != lines[1] {
		t.Errorf("composite() = %q, want the background untouched", got)
	}
}
