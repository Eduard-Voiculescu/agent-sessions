package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

// startRename opens the prompt over one session. The target is captured by value
// for the same reason the palette captures its own: a tick re-sorts the rows
// while somebody is typing, and a rename resolved from the cursor afterwards
// lands on whichever session slid into the slot.
//
// The prompt is prefilled with the name on screen rather than left empty: a
// rename is usually an edit of what is already there, and retyping a sentence to
// change one word of it is the wrong price.
func (m model) startRename(target session.Session) model {
	if m.rename == nil {
		m.status = "renaming is unavailable"
		return m
	}

	m.renaming = true
	m.renameTarget = target
	m.renameText = target.Name
	m.status = ""
	return m
}

func (m model) closeRename() model {
	m.renaming = false
	m.renameText = ""
	m.renameTarget = session.Session{}
	return m
}

func (m model) updateRenaming(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		return m.closeRename(), nil

	case tea.KeyCtrlU:
		m.renameText = ""
		return m, nil

	case tea.KeyBackspace:
		if m.renameText != "" {
			m.renameText = trimLastRune(m.renameText)
		}
		return m, nil

	// KeySpace carries the space in Runes, and a name is usually several words.
	case tea.KeyRunes, tea.KeySpace:
		m.renameText += printable(msg.Runes)
		return m, nil

	case tea.KeyEnter:
		return m.commitRename()
	}

	return m, nil
}

// commitRename hands the name to the store and reloads, so the row redraws under
// its new name rather than waiting out a tick. An emptied prompt clears the
// custom name: the row falls back to whatever the agent calls it, which is the
// same thing the palette's clear entry does.
//
// A refusal keeps the prompt open holding the text — a rejected character is
// fixable, and retyping the rest of the name is the wrong price for it.
func (m model) commitRename() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.renameText)
	target := m.renameTarget

	if err := m.rename(target, name); err != nil {
		m.status = err.Error()
		return m, nil
	}

	m = m.closeRename()
	m.status = renamed(target, name)
	return m.reloadSessions(), nil
}

func renamed(target session.Session, name string) string {
	if name == "" {
		return "cleared the name on " + target.ShortID()
	}
	return "renamed to " + name
}

// renamePrompt is the footer line. It names the session being renamed, because
// the palette can open this over a row the cursor has since left.
func (m model) renameLine() string {
	return gutter("rename › "+m.renameText+"_", m.width)
}
