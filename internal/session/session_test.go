package session

import (
	"testing"
	"time"
)

var (
	older  = time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	newer  = time.Date(2026, 8, 18, 11, 0, 0, 0, time.UTC)
	newest = time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
)

func TestMerge(t *testing.T) {
	tests := []struct {
		name    string
		history []Session
		live    []Session
		want    []Session
	}{
		{
			name:    "live overlays name cwd and status",
			history: []Session{{Agent: "claude", ID: "a", Name: "old title", Cwd: "/old", LastActive: older}},
			live:    []Session{{Agent: "claude", ID: "a", Name: "webapp-8e", Cwd: "/new", Status: "running", Live: true, PID: 7, LastActive: newer}},
			want: []Session{{
				Agent: "claude", ID: "a", Name: "webapp-8e", Cwd: "/new",
				Status: "running", Live: true, PID: 7, LastActive: newer,
			}},
		},
		{
			name:    "live row promotes a history title over the registry name",
			history: []Session{{Agent: "claude", ID: "a", Name: "abc-2730 export report as PDF", Title: "abc-2730 export report as PDF", Cwd: "/old", LastActive: older}},
			live:    []Session{{Agent: "claude", ID: "a", Name: "billing-6b", Cwd: "/new", Status: "running", Live: true, PID: 7, LastActive: newer}},
			want: []Session{{
				Agent: "claude", ID: "a", Name: "abc-2730 export report as PDF", Title: "abc-2730 export report as PDF", Cwd: "/new",
				Status: "running", Live: true, PID: 7, LastActive: newer,
			}},
		},
		{
			// This is the case a naive cmp.Or(s.Name, l.Name) flip gets wrong: with
			// no title, a short id from history must still lose to the registry name.
			name:    "live row with no title still beats a history name that is a short id",
			history: []Session{{Agent: "claude", ID: "a", Name: "7b31d3e6", Cwd: "/old", LastActive: older}},
			live:    []Session{{Agent: "claude", ID: "a", Name: "webapp-90", Cwd: "/new", Status: "running", Live: true, PID: 7, LastActive: newer}},
			want: []Session{{
				Agent: "claude", ID: "a", Name: "webapp-90", Cwd: "/new",
				Status: "running", Live: true, PID: 7, LastActive: newer,
			}},
		},
		{
			name:    "live row with no title still beats a history name that is a cleaned prompt",
			history: []Session{{Agent: "claude", ID: "a", Name: "fix the flaky test in CI", Cwd: "/old", LastActive: older}},
			live:    []Session{{Agent: "claude", ID: "a", Name: "webapp-90", Cwd: "/new", Status: "running", Live: true, PID: 7, LastActive: newer}},
			want: []Session{{
				Agent: "claude", ID: "a", Name: "webapp-90", Cwd: "/new",
				Status: "running", Live: true, PID: 7, LastActive: newer,
			}},
		},
		{
			name:    "history-only row with a title is unaffected by merge",
			history: []Session{{Agent: "claude", ID: "a", Name: "abc-2730 export report as PDF", Title: "abc-2730 export report as PDF", Cwd: "/repo", LastActive: newer}},
			live:    nil,
			want:    []Session{{Agent: "claude", ID: "a", Name: "abc-2730 export report as PDF", Title: "abc-2730 export report as PDF", Cwd: "/repo", LastActive: newer}},
		},
		{
			name:    "history-only row with no title is unaffected by merge",
			history: []Session{{Agent: "claude", ID: "a", Name: "7b31d3e6", Cwd: "/repo", LastActive: newer}},
			live:    nil,
			want:    []Session{{Agent: "claude", ID: "a", Name: "7b31d3e6", Cwd: "/repo", LastActive: newer}},
		},
		{
			name: "history-only fields survive the overlay",
			history: []Session{{
				Agent: "claude", ID: "a", Name: "title", Cwd: "/repo",
				GitBranch: "eng-3140-x", Transcript: "/t/a.jsonl", LastActive: newer,
			}},
			live: []Session{{Agent: "claude", ID: "a", Live: true, PID: 7, Status: "running", LastActive: older}},
			want: []Session{{
				Agent: "claude", ID: "a", Name: "title", Cwd: "/repo",
				GitBranch: "eng-3140-x", Transcript: "/t/a.jsonl",
				Live: true, PID: 7, Status: "running", LastActive: newer,
			}},
		},
		{
			name:    "live session with no transcript is appended",
			history: nil,
			live:    []Session{{Agent: "claude", ID: "b", Name: "fresh", Live: true, PID: 9, LastActive: newer}},
			want:    []Session{{Agent: "claude", ID: "b", Name: "fresh", Live: true, PID: 9, LastActive: newer}},
		},
		{
			name:    "same id under different agents does not collide",
			history: []Session{{Agent: "claude", ID: "a", Name: "claude one", LastActive: older}},
			live:    []Session{{Agent: "codex", ID: "a", Name: "codex one", Live: true, PID: 3, LastActive: newer}},
			want: []Session{
				{Agent: "codex", ID: "a", Name: "codex one", Live: true, PID: 3, LastActive: newer},
				{Agent: "claude", ID: "a", Name: "claude one", LastActive: older},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Merge(tt.history, tt.live)
			Sort(got)

			if len(got) != len(tt.want) {
				t.Fatalf("Merge() returned %d sessions, want %d: %+v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Merge()[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestSortPutsLiveFirstThenNewest(t *testing.T) {
	got := []Session{
		{ID: "history-old", LastActive: older},
		{ID: "live-old", Live: true, LastActive: older},
		{ID: "history-new", LastActive: newest},
		{ID: "live-new", Live: true, LastActive: newest},
	}
	Sort(got)

	want := []string{"live-new", "live-old", "history-new", "history-old"}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("Sort()[%d] = %q, want %q (full order %v)", i, got[i].ID, id, ids(got))
		}
	}
}

func TestShortID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{name: "uuid is truncated", id: "8c4d5e6f-0000-4000-8000-000000000004", want: "8c4d5e6f"},
		{name: "short id is returned whole", id: "abc", want: "abc"},
		{name: "empty id stays empty", id: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Session{ID: tt.id}).ShortID(); got != tt.want {
				t.Errorf("ShortID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAge(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "zero time has no age", at: time.Time{}, want: "-"},
		{name: "seconds read as now", at: time.Now().Add(-30 * time.Second), want: "now"},
		{name: "minutes", at: time.Now().Add(-5 * time.Minute), want: "5m"},
		{name: "hours", at: time.Now().Add(-3 * time.Hour), want: "3h"},
		{name: "days", at: time.Now().Add(-50 * 24 * time.Hour), want: "50d"},
		{name: "the last three-digit day count is exact", at: time.Now().Add(-999 * 24 * time.Hour), want: "999d"},
		{name: "a four-digit day count saturates", at: time.Now().Add(-5000 * 24 * time.Hour), want: "999+"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Age(tt.at)
			if got != tt.want {
				t.Errorf("Age(%v) = %q, want %q", tt.at, got, tt.want)
			}
			// The picker's age column is four cells wide and pads rather than
			// wraps, so anything longer widens every row past the terminal.
			if len(got) > 4 {
				t.Errorf("Age(%v) = %q, wider than the four cells the age column has", tt.at, got)
			}
		})
	}
}

func ids(sessions []Session) []string {
	out := make([]string, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s.ID)
	}
	return out
}
