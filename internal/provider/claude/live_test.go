package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeRegistry(t *testing.T, dir, name, body string) {
	t.Helper()
	sessions := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessions, name), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

const liveEntry = `{
  "pid": 58854,
  "sessionId": "8c4d5e6f-0000-4000-8000-000000000004",
  "cwd": "/Users/dev/git/webapp",
  "startedAt": 1787063093762,
  "version": "2.1.234",
  "kind": "interactive",
  "name": "webapp-8e",
  "status": "waiting",
  "waitingFor": "input needed",
  "updatedAt": 1787063532873
}`

const runningEntry = `{
  "pid": 60790,
  "sessionId": "5e1f2a3b-0000-4000-8000-000000000001",
  "cwd": "/Users/dev/git/ledger",
  "kind": "interactive",
  "name": "ledger-4a",
  "status": "running",
  "updatedAt": 1787063532873
}`

func TestReadRegistry(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		alive      func(int) bool
		wantIDs    []string
		wantStatus string
	}{
		{
			name:       "waiting session reports what it waits for",
			files:      map[string]string{"58854.json": liveEntry},
			alive:      func(int) bool { return true },
			wantIDs:    []string{"8c4d5e6f-0000-4000-8000-000000000004"},
			wantStatus: "waiting: input needed",
		},
		{
			name:       "running session reports its status verbatim",
			files:      map[string]string{"60790.json": runningEntry},
			alive:      func(int) bool { return true },
			wantIDs:    []string{"5e1f2a3b-0000-4000-8000-000000000001"},
			wantStatus: "running",
		},
		{
			name:    "stale file whose pid is gone is dropped",
			files:   map[string]string{"58854.json": liveEntry},
			alive:   func(int) bool { return false },
			wantIDs: nil,
		},
		{
			name: "malformed file is skipped without losing the good one",
			files: map[string]string{
				"1.json":     "{not json",
				"58854.json": liveEntry,
			},
			alive:   func(int) bool { return true },
			wantIDs: []string{"8c4d5e6f-0000-4000-8000-000000000004"},
		},
		{
			name: "key files and entries without a session id are ignored",
			files: map[string]string{
				"58854.abc.key": "not json at all",
				"2.json":        `{"pid":2,"cwd":"/x"}`,
			},
			alive:   func(int) bool { return true },
			wantIDs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range tt.files {
				writeRegistry(t, dir, name, body)
			}

			got, err := readRegistry(dir, tt.alive)
			if err != nil {
				t.Fatalf("readRegistry() error = %v", err)
			}
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("readRegistry() returned %d sessions, want %d: %+v", len(got), len(tt.wantIDs), got)
			}
			for i, id := range tt.wantIDs {
				if got[i].ID != id {
					t.Errorf("session[%d].ID = %q, want %q", i, got[i].ID, id)
				}
				if got[i].Agent != "claude" {
					t.Errorf("session[%d].Agent = %q, want %q", i, got[i].Agent, "claude")
				}
				if !got[i].Live {
					t.Errorf("session[%d].Live = false, want true", i)
				}
			}
			if tt.wantStatus != "" && got[0].Status != tt.wantStatus {
				t.Errorf("session[0].Status = %q, want %q", got[0].Status, tt.wantStatus)
			}
		})
	}
}

func TestReadRegistryMissingDirectoryIsNotAnError(t *testing.T) {
	got, err := readRegistry(filepath.Join(t.TempDir(), "absent"), func(int) bool { return true })
	if err != nil {
		t.Fatalf("readRegistry() error = %v, want nil for a missing directory", err)
	}
	if len(got) != 0 {
		t.Errorf("readRegistry() = %+v, want no sessions", got)
	}
}

func TestPidAliveForCurrentProcess(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Error("pidAlive(os.Getpid()) = false, want true")
	}
}

func TestReadRegistryCarriesTheStartTimeThrough(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-90 * time.Minute).Truncate(time.Millisecond)
	record := fmt.Sprintf(`{"pid":4242,"sessionId":"abc","name":"a session","status":"idle","startedAt":%d}`, started.UnixMilli())
	if err := os.WriteFile(filepath.Join(sessions, "abc.json"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readRegistry(dir, func(int) bool { return true })
	if err != nil {
		t.Fatalf("readRegistry() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("readRegistry() returned %d sessions, want 1", len(got))
	}
	if !got[0].StartedAt.Equal(started) {
		t.Errorf("StartedAt = %s, want %s", got[0].StartedAt, started)
	}
}

// A record with no startedAt must arrive as the zero time rather than as the
// epoch: the kill action refuses what it cannot confirm, and 1970 would read as
// a fifty-year-old process instead of as an unknown one.
func TestReadRegistryLeavesAMissingStartTimeZero(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessions, "abc.json"),
		[]byte(`{"pid":4242,"sessionId":"abc","name":"a session","status":"idle"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readRegistry(dir, func(int) bool { return true })
	if err != nil {
		t.Fatalf("readRegistry() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("readRegistry() returned %d sessions, want 1", len(got))
	}
	if !got[0].StartedAt.IsZero() {
		t.Errorf("StartedAt = %s, want the zero time", got[0].StartedAt)
	}
}
