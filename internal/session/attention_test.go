package session

import "testing"

func TestClassify(t *testing.T) {
	tests := []struct {
		name      string
		sessions  []Session
		wantWorst string
		wantCount int
	}{
		{
			name:      "nothing running",
			sessions:  []Session{{Agent: "claude", ID: "a"}},
			wantWorst: AttentionNone,
			wantCount: 0,
		},
		{
			name:      "a live session with no status reads as busy",
			sessions:  []Session{{Agent: "claude", ID: "a", Live: true, PID: 1}},
			wantWorst: AttentionBusy,
			wantCount: 1,
		},
		{
			name:      "waiting outranks busy",
			sessions:  []Session{{ID: "a", Live: true, PID: 1, Status: "busy"}, {ID: "b", Live: true, PID: 2, Status: "waiting: input needed"}},
			wantWorst: AttentionWaiting,
			wantCount: 1,
		},
		{
			name:      "the count is how many are in the worst class",
			sessions:  []Session{{ID: "a", Live: true, PID: 1, Status: "waiting: input needed"}, {ID: "b", Live: true, PID: 2, Status: "waiting: permission"}},
			wantWorst: AttentionWaiting,
			wantCount: 2,
		},
		{
			name:      "an idle live session is idle, not busy",
			sessions:  []Session{{ID: "a", Live: true, PID: 1, Status: "idle"}},
			wantWorst: AttentionIdle,
			wantCount: 1,
		},
		{
			name:      "a blocked job outranks a busy one",
			sessions:  []Session{{ID: "a", JobState: "working"}, {ID: "b", JobState: "blocked"}},
			wantWorst: AttentionBlocked,
			wantCount: 1,
		},
		// A status is whatever the agent wrote, so an unrecognised one must read
		// as work in progress: calling it attention would cry wolf, and calling
		// it idle would hide a working agent.
		{
			name:      "an unrecognised status reads as busy",
			sessions:  []Session{{ID: "a", Live: true, PID: 1, Status: "compacting context"}},
			wantWorst: AttentionBusy,
			wantCount: 1,
		},
		{
			name:      "case does not decide it",
			sessions:  []Session{{ID: "a", Live: true, PID: 1, Status: "Waiting: Input Needed"}},
			wantWorst: AttentionWaiting,
			wantCount: 1,
		},
		{
			name:      "a live session outranks its own stale job record",
			sessions:  []Session{{ID: "a", Live: true, PID: 1, Status: "idle", JobState: "blocked"}},
			wantWorst: AttentionIdle,
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.sessions)
			if got.Worst != tt.wantWorst || got.Count != tt.wantCount {
				t.Errorf("Classify() = %+v, want worst %q count %d", got, tt.wantWorst, tt.wantCount)
			}
		})
	}
}

func TestClassifyEmpty(t *testing.T) {
	if got := Classify(nil); got.Worst != AttentionNone || got.Count != 0 {
		t.Errorf("Classify(nil) = %+v, want none and zero", got)
	}
}
