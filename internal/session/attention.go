package session

import "strings"

// Attention is how a set of sessions reads at a glance: the class that most
// wants a human, and how many sessions are in it. It is computed here rather
// than by each consumer so the picker, the feed and anything reading the feed
// cannot rank the same statuses differently.
type Attention struct {
	Worst string `json:"worst"`
	Count int    `json:"attention"`
}

// The classes, in the order they outrank each other.
const (
	AttentionWaiting = "waiting"
	AttentionBlocked = "blocked"
	AttentionBusy    = "busy"
	AttentionIdle    = "idle"
	AttentionNone    = "none"
)

var attentionOrder = []string{AttentionWaiting, AttentionBlocked, AttentionBusy, AttentionIdle}

func Classify(sessions []Session) Attention {
	counts := map[string]int{}
	for _, s := range sessions {
		counts[class(s)]++
	}

	for _, name := range attentionOrder {
		if counts[name] > 0 {
			return Attention{Worst: name, Count: counts[name]}
		}
	}
	return Attention{Worst: AttentionNone}
}

// class is one session's own state. A status is self-reported and its vocabulary
// belongs to the agent, so only two spellings are recognised and everything else
// live counts as work in progress: an unanticipated status must never read as
// wanting a human, and must never hide a working agent either.
func class(s Session) string {
	if s.Live {
		status := strings.ToLower(strings.TrimSpace(s.Status))
		switch {
		case strings.HasPrefix(status, AttentionWaiting):
			return AttentionWaiting
		case status == AttentionIdle:
			return AttentionIdle
		default:
			return AttentionBusy
		}
	}

	// A job runs under the daemon and never appears in the live registry, so its
	// own record is the only thing that can speak for it — but a live process
	// outranks it above, because a job record outlives the run it describes.
	switch s.JobState {
	case "blocked":
		return AttentionBlocked
	case "working":
		return AttentionBusy
	}
	return AttentionNone
}
