package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestModalTopBorderCarriesTheTitleLeftAndTheKeyRight(t *testing.T) {
	got := boxTopWith("start a session", "esc", 44)

	if width := lipgloss.Width(got); width != 44 {
		t.Errorf("top border is %d cells wide, want 44: %q", width, got)
	}
	if !strings.HasPrefix(got, "╭─ start a session ") {
		t.Errorf("the title is not at the left: %q", got)
	}
	if !strings.HasSuffix(got, " esc ─╮") {
		t.Errorf("the key is not at the right: %q", got)
	}
}

// The title names what the modal is for, so it is the half that survives a
// border too narrow for both.
func TestModalTopBorderDropsTheKeyBeforeTheTitle(t *testing.T) {
	got := boxTopWith("start a session", "esc", 20)

	if width := lipgloss.Width(got); width != 20 {
		t.Errorf("top border is %d cells wide, want 20: %q", width, got)
	}
	if strings.Contains(got, "esc") {
		t.Errorf("the key is still drawn on a border with no room for it: %q", got)
	}
	if !strings.Contains(got, "start") {
		t.Errorf("the title was dropped instead of the key: %q", got)
	}
}

func TestModalEntryRightAlignsItsNote(t *testing.T) {
	got := modalLines([]modalItem{{label: "~/git/acme/api", note: "3 sessions"}}, 40)

	if len(got) != 1 {
		t.Fatalf("modalLines() returned %d lines, want 1", len(got))
	}
	if width := lipgloss.Width(got[0]); width != 40 {
		t.Errorf("entry is %d cells wide, want the full inner 40: %q", width, got[0])
	}
	if !strings.HasSuffix(got[0], "3 sessions") {
		t.Errorf("the note is not at the right edge: %q", got[0])
	}
}

// A note is an annotation; a label is the thing being chosen. The label keeps
// the room when there is not enough for both.
func TestModalEntryDropsTheNoteWhenTheLabelNeedsTheRoom(t *testing.T) {
	got := modalLines([]modalItem{{label: "~/git/acme/some/rather/long/directory/name", note: "3 sessions"}}, 24)

	if width := lipgloss.Width(got[0]); width != 24 {
		t.Errorf("entry is %d cells wide, want 24: %q", width, got[0])
	}
	if strings.Contains(got[0], "3 sessions") {
		t.Errorf("the note survived at the label's expense: %q", got[0])
	}
}

// The selected entry is a bar across the modal, not a marker on one word: at a
// glance the eye finds a filled row long before it finds an arrow.
func TestModalSelectedEntryFillsTheRow(t *testing.T) {
	got := modalLines([]modalItem{{label: "claude", selected: true}}, 30)

	if width := lipgloss.Width(got[0]); width != 30 {
		t.Errorf("selected entry is %d cells wide, want the full inner 30: %q", width, got[0])
	}
	// Compared against the style's own render rather than against an escape
	// sequence: the tests run with no TTY, where lipgloss emits none at all.
	if want := selectedBarStyle.Render(pad("▸ claude", 30)); got[0] != want {
		t.Errorf("selected entry = %q, want the whole row rendered as a bar: %q", got[0], want)
	}
	if !strings.Contains(got[0], "▸") {
		t.Errorf("the selected entry lost its marker, which the tests for both modals look for: %q", got[0])
	}
}

func TestModalDrawsHeadersAndBlanks(t *testing.T) {
	got := modalLines([]modalItem{{label: "go to", header: true}, {blank: true}, {label: "open PR"}}, 20)

	if len(got) != 3 {
		t.Fatalf("modalLines() returned %d lines, want 3", len(got))
	}
	if !strings.Contains(got[0], "go to") {
		t.Errorf("the header is missing: %q", got[0])
	}
	if strings.TrimSpace(got[1]) != "" {
		t.Errorf("the blank line is not blank: %q", got[1])
	}
}

func TestModalBoxFramesItsContentToOneWidth(t *testing.T) {
	got := modalBox("actions", "esc", []string{"one", "two"}, 30)

	if len(got) != 4 {
		t.Fatalf("modalBox() returned %d lines, want two borders around two lines", len(got))
	}
	for i, line := range got {
		if width := lipgloss.Width(line); width != 30 {
			t.Errorf("line %d is %d cells wide, want 30: %q", i, width, line)
		}
	}
	if !strings.HasPrefix(got[3], "╰") {
		t.Errorf("the box has no bottom border: %q", got[3])
	}
}

// A modal is only worth drawing while the frame behind it is still readable
// around it. Below that, the chooser takes the whole screen instead — the same
// cheapest-first surrender every other frame in this package makes.
func TestModalFrameGivesUpOnATerminalWithNoRoomForABox(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
	}{
		{name: "too narrow", width: 30, height: 24},
		{name: "too short", width: 100, height: 6},
		{name: "unset", width: 0, height: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if f := modalFrameFor(tt.width, tt.height, 2, 10); f.width != 0 {
				t.Errorf("modalFrameFor() = %+v, want a zero width asking for the full-screen view", f)
			}
		})
	}
}

func TestModalFrameLeavesRoomForTheBackgroundAndTheChrome(t *testing.T) {
	f := modalFrameFor(120, 40, 4, 40)

	if f.width > 120-8 {
		t.Errorf("box width %d leaves no background either side of it", f.width)
	}
	if f.items < 1 {
		t.Errorf("modalFrameFor() = %+v, want room for at least one entry", f)
	}
	// Two borders, the chrome and the entries have to fit inside the terminal
	// with lines left over for the frame behind.
	if total := f.items + 4 + 2; total > 40-4 {
		t.Errorf("box is %d lines tall in a 40-line terminal", total)
	}
}

// A box sized to the terminal rather than to its list leaves a field of empty
// rows under two agents. The window is what the list needs, up to what fits.
func TestModalCapacityShrinksToTheList(t *testing.T) {
	m := newModel(Config{Sessions: startSessions(), StartAgents: []string{"claude", "opencode"}})
	m.width, m.height = 110, 40
	m = m.openStart()

	if got := m.startCapacity(); got != 2 {
		t.Errorf("startCapacity() = %d, want the 2 agents it has to draw", got)
	}

	view := m.View()
	if lines := strings.Count(view, "\n"); lines != 39 {
		t.Errorf("View() drew %d newlines, want the terminal's own 39", lines)
	}
	// Between the borders: the two agents, the blank and the hint.
	if strings.Contains(view, "opencode                                                      │\n") &&
		!strings.Contains(view, "╰") {
		t.Error("the box has no bottom border")
	}
}
