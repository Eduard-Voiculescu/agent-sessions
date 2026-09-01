// Package session holds the agent-agnostic session record and the operations that combine records from different sources.
package session

import (
	"cmp"
	"fmt"
	"slices"
	"time"
)

type Session struct {
	Agent string `json:"agent"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`
	Cwd   string `json:"cwd"`
	// Label is a name the owner gave this session. It outranks every derived name,
	// including an agent's own generated title: a name somebody typed is the only
	// one they chose. Empty means nobody has renamed this session, which is the
	// common case and falls back to whatever the agent calls it.
	Label      string    `json:"label,omitempty"`
	GitBranch  string    `json:"gitBranch,omitempty"`
	Transcript string    `json:"transcript,omitempty"`
	Live       bool      `json:"live"`
	PID        int       `json:"pid,omitempty"`
	Status     string    `json:"status,omitempty"`
	LastActive time.Time `json:"lastActive"`
	// StartedAt is when the live registry says the process began. It is the only
	// thing that separates this pid from a later process that inherited the
	// number, so the kill action refuses to signal without it.
	StartedAt time.Time `json:"startedAt,omitempty"`

	// JobState, Detail and Tokens come from a background agent's own record of
	// itself rather than from a running process, which is why they sit apart
	// from Status: Status is what a live registry says about a pid, and the
	// liveness-stripped baseline that ticks merge against clears it every time.
	// A job's record survives that, because a finished job still has something
	// true to say about the transcript it left behind.
	JobState string `json:"jobState,omitempty"`
	// Detail is the agent's own latest progress line, rewritten as it works. It
	// is self-reported, not derived: an agent writes whatever it likes there.
	Detail string `json:"detail,omitempty"`
	Tokens int    `json:"tokens,omitempty"`
}

// Key identifies a session across agents. Two agents may mint the same id, so
// the agent name is part of the identity.
type Key struct {
	Agent string
	ID    string
}

func (s Session) Key() Key {
	return Key{Agent: s.Agent, ID: s.ID}
}

func (s Session) ShortID() string {
	if len(s.ID) <= 8 {
		return s.ID
	}
	return s.ID[:8]
}

// Merge overlays running-process records onto transcript records. History
// supplies what only the transcript knows (branch, transcript path); live
// supplies liveness and anything it states non-empty.
func Merge(history, live []Session) []Session {
	merged := slices.Clone(history)
	index := make(map[Key]int, len(merged))
	for i, s := range merged {
		index[s.Key()] = i
	}

	for _, l := range live {
		i, ok := index[l.Key()]
		if !ok {
			index[l.Key()] = len(merged)
			merged = append(merged, l)
			continue
		}

		s := merged[i]
		s.Live = true
		s.PID = l.PID
		s.Status = l.Status
		s.StartedAt = l.StartedAt
		// A label arrives on whichever side was loaded when the rename was made:
		// history for a session with a transcript, live for one renamed before it
		// had written one.
		//
		// Title is kept apart from Name so it can be promoted here without
		// disturbing a Name that fell back to a cleaned prompt or a short id —
		// only an actual ai-title should ever outrank the live registry's name.
		if label := cmp.Or(s.Label, l.Label); label != "" {
			s.Label, s.Name = label, label
		} else {
			s.Name = cmp.Or(s.Title, l.Name, s.Name)
		}
		s.Cwd = cmp.Or(l.Cwd, s.Cwd)
		if l.LastActive.After(s.LastActive) {
			s.LastActive = l.LastActive
		}
		merged[i] = s
	}

	return merged
}

// Sort orders live sessions ahead of the rest, each group newest first.
func Sort(sessions []Session) {
	slices.SortStableFunc(sessions, func(a, b Session) int {
		if a.Live != b.Live {
			if a.Live {
				return -1
			}
			return 1
		}
		return b.LastActive.Compare(a.LastActive)
	})
}

// Age renders how long ago t was in a fixed-width-friendly form; a zero time,
// which is what a session with no known activity carries, renders as "-". Past
// 999 days it saturates rather than widening: the picker's age column is four
// cells wide and truncates what it is given, so "1000d" would arrive as "100…"
// and read as a hundred days. "999+" says more-than instead of lying by a factor
// of ten.
func Age(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		days := int(d.Hours() / 24)
		if days > 999 {
			return "999+"
		}
		return fmt.Sprintf("%dd", days)
	}
}
