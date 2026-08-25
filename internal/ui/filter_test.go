package ui

import (
	"testing"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

func TestFilter(t *testing.T) {
	all := []session.Session{
		{Agent: "claude", ID: "1", Name: "Review CI performance", Cwd: "/Users/dev/git/webapp", GitBranch: "ci-optimizations"},
		{Agent: "codex", ID: "2", Name: "Fix search ranking endpoints", Cwd: "/Users/dev/git/billing", GitBranch: "eng-3140-search ranking"},
		{Agent: "claude", ID: "3", Name: "Build sessions CLI", Cwd: "/Users/dev/git/personal/agent-sessions", GitBranch: "develop"},
	}

	tests := []struct {
		name    string
		query   string
		wantIDs []string
	}{
		{name: "empty query keeps everything", query: "", wantIDs: []string{"1", "2", "3"}},
		{name: "matches name case-insensitively", query: "review ci", wantIDs: []string{"1"}},
		{name: "matches directory", query: "billing", wantIDs: []string{"2"}},
		{name: "matches branch", query: "eng-3140", wantIDs: []string{"2"}},
		{name: "matches agent", query: "codex", wantIDs: []string{"2"}},
		{name: "agent narrows another field", query: "claude review", wantIDs: []string{"1"}},
		{name: "no match yields nothing", query: "zzzz", wantIDs: nil},
		{name: "all terms must match", query: "search ranking webapp", wantIDs: nil},
		{name: "terms may match different fields", query: "search ranking billing", wantIDs: []string{"2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Filter(all, tt.query)
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("Filter(%q) returned %d sessions, want %d: %+v", tt.query, len(got), len(tt.wantIDs), got)
			}
			for i, id := range tt.wantIDs {
				if got[i].ID != id {
					t.Errorf("Filter(%q)[%d].ID = %q, want %q", tt.query, i, got[i].ID, id)
				}
			}
		})
	}
}

// A background agent's progress line is the only text that says what it is
// doing, and it is the natural thing to search for.
func TestFilterMatchesABackgroundAgentsProgressLine(t *testing.T) {
	sessions := []session.Session{
		{Agent: "claude", Name: "job row", Detail: "Verifying all 5 fixed cases across 4 runs"},
		{Agent: "claude", Name: "other row"},
	}

	kept := Filter(sessions, "fixed cases")
	if len(kept) != 1 || kept[0].Name != "job row" {
		t.Errorf("Filter() = %+v, want only the row whose progress line matches", kept)
	}
}
