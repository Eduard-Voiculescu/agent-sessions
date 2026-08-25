package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

type registryEntry struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	Cwd        string `json:"cwd"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	WaitingFor string `json:"waitingFor"`
	UpdatedAt  int64  `json:"updatedAt"`
	StartedAt  int64  `json:"startedAt"`
}

func (e registryEntry) status() string {
	if e.Status == "waiting" && e.WaitingFor != "" {
		return "waiting: " + e.WaitingFor
	}
	return e.Status
}

// startedAt is the zero time when the record carries no stamp, never the epoch:
// the kill action refuses what it cannot confirm, and 1970 would read as a
// fifty-year-old process rather than as an unknown one.
func (e registryEntry) startedAt() time.Time {
	if e.StartedAt == 0 {
		return time.Time{}
	}
	return time.UnixMilli(e.StartedAt)
}

func (e registryEntry) lastActive() time.Time {
	stamp := e.UpdatedAt
	if stamp == 0 {
		stamp = e.StartedAt
	}
	if stamp == 0 {
		return time.Time{}
	}
	return time.UnixMilli(stamp)
}

func readRegistry(dir string, alive func(int) bool) ([]session.Session, error) {
	root := filepath.Join(dir, "sessions")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading claude session registry %s: %w", root, err)
	}

	sessions := make([]session.Session, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		raw, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			continue
		}

		var parsed registryEntry
		if err := json.Unmarshal(raw, &parsed); err != nil {
			continue
		}
		if parsed.SessionID == "" || !alive(parsed.PID) {
			continue
		}

		sessions = append(sessions, session.Session{
			Agent:      "claude",
			ID:         parsed.SessionID,
			Name:       parsed.Name,
			Cwd:        parsed.Cwd,
			Live:       true,
			PID:        parsed.PID,
			Status:     parsed.status(),
			LastActive: parsed.lastActive(),
			StartedAt:  parsed.startedAt(),
		})
	}

	return sessions, nil
}

// pidAlive reports whether a process exists. EPERM means it exists under
// another user, which still counts as alive; only ESRCH means gone.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
