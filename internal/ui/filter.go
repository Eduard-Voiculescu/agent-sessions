// Package ui renders the interactive session picker.
package ui

import (
	"strings"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

// Filter keeps the sessions matching every whitespace-separated term of query,
// case-insensitively, across agent, name, directory, branch and a background
// agent's own progress line.
func Filter(sessions []session.Session, query string) []session.Session {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return sessions
	}

	kept := make([]session.Session, 0, len(sessions))
	for _, s := range sessions {
		haystack := strings.ToLower(strings.Join([]string{s.Agent, s.Name, s.Cwd, s.GitBranch, s.Detail}, " "))
		if matchesAll(haystack, terms) {
			kept = append(kept, s)
		}
	}
	return kept
}

func matchesAll(haystack string, terms []string) bool {
	for _, term := range terms {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}
