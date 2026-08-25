package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

// jobsDir holds one directory per background agent, each with the agent's own
// record of itself. It is a third store beside the live registry and the
// transcripts: a background agent runs under the daemon rather than on a tty,
// so it never appears in the live registry at all, and without this its rows
// look like any other piece of history.
const jobsDir = "jobs"

// jobStateFile is read; timeline.jsonl beside it is the append-only log of
// every progress line the agent ever wrote, and is deliberately not read — the
// current line is what a list can show, and the log runs to tens of thousands
// of records.
const jobStateFile = "state.json"

// jobRecord is the subset of a job's state.json this tool reads.
type jobRecord struct {
	Name string `json:"name"`
	// Detail is the agent's own latest progress line.
	Detail string `json:"detail"`
	// State is "working", "blocked" or "done".
	State string `json:"state"`
	// Output carries a closing summary once the agent finishes. When it is
	// present it supersedes Detail, which is otherwise left holding whatever the
	// agent last said mid-flight.
	Output *struct {
		Result string `json:"result"`
	} `json:"output"`
	Tokens int `json:"tokens"`
	// SessionID is the transcript the job started on. ResumeSessionID is the one
	// it is on now, and the two differ once a job has been resumed: the job's
	// name is an ai-title, so every transcript it ever occupied carries the same
	// one, and attaching the record to both would leave several identical rows
	// all claiming to be the live one.
	SessionID       string `json:"sessionId"`
	ResumeSessionID string `json:"resumeSessionId"`
}

// session is the transcript id this record describes: the one the job is on
// now.
func (r jobRecord) session() string {
	if r.ResumeSessionID != "" {
		return r.ResumeSessionID
	}
	return r.SessionID
}

// detail prefers the closing summary over the last mid-flight progress line,
// which is what `claude agents` itself shows.
func (r jobRecord) detail() string {
	if r.Output != nil && r.Output.Result != "" {
		return r.Output.Result
	}
	return r.Detail
}

// readJobs indexes every background agent's record by the transcript id it is
// currently on. A missing jobs directory is not an error: a machine that has
// never run a background agent has none.
func readJobs(dir string) (map[string]jobRecord, error) {
	entries, err := os.ReadDir(filepath.Join(dir, jobsDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", jobsDir, err)
	}

	jobs := make(map[string]jobRecord, len(entries))
	var unread int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		raw, err := os.ReadFile(filepath.Join(dir, jobsDir, entry.Name(), jobStateFile))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			unread++
			continue
		}

		var record jobRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			unread++
			continue
		}
		if id := record.session(); id != "" {
			jobs[id] = record
		}
	}

	if unread > 0 {
		return jobs, fmt.Errorf("%d of %d job records could not be read", unread, len(entries))
	}
	return jobs, nil
}

// applyJobs folds each background agent's record onto the session it is on. The
// name is not taken: a job's name and its transcript's ai-title are the same
// generated string, so promoting it would only overwrite a value with itself,
// and on a transcript whose own title differs it would be the wrong one.
func applyJobs(sessions []session.Session, jobs map[string]jobRecord) {
	if len(jobs) == 0 {
		return
	}
	for i, s := range sessions {
		record, ok := jobs[s.ID]
		if !ok {
			continue
		}
		sessions[i].JobState = record.State
		// A progress line is whatever the agent chose to write, newlines
		// included.
		sessions[i].Detail = oneLine(record.detail())
		sessions[i].Tokens = record.Tokens
	}
}
