package claude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

func writeJob(t *testing.T, dir, short, body string) {
	t.Helper()
	jobDir := filepath.Join(dir, "jobs", short)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "state.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadJobsIndexesEveryFieldTheListShows(t *testing.T) {
	dir := t.TempDir()
	writeJob(t, dir, "5e1f2a3b", `{
		"name": "search reindex to the new analyzer",
		"detail": "Verifying all 5 fixed cases across 4 runs",
		"state": "working",
		"tokens": 43223,
		"output": null,
		"sessionId": "5e1f2a3b-0000-4000-8000-000000000001",
		"resumeSessionId": "5e1f2a3b-0000-4000-8000-000000000001"
	}`)

	jobs, err := readJobs(dir)
	if err != nil {
		t.Fatalf("readJobs() error = %v", err)
	}

	job, ok := jobs["5e1f2a3b-0000-4000-8000-000000000001"]
	if !ok {
		t.Fatalf("readJobs() = %v, want it keyed by the transcript id", jobs)
	}
	if job.State != "working" {
		t.Errorf("State = %q, want %q", job.State, "working")
	}
	if job.detail() != "Verifying all 5 fixed cases across 4 runs" {
		t.Errorf("detail() = %q, want the progress line", job.detail())
	}
	if job.Tokens != 43223 {
		t.Errorf("Tokens = %d, want 43223", job.Tokens)
	}
}

// A finished job leaves `detail` holding whatever it last said mid-flight,
// which is why `claude agents` shows the closing summary instead once there is
// one. Reading the wrong one shows a stale "Verifying…" beside a job that has
// been done for two weeks.
func TestReadJobsPrefersTheClosingSummaryOverTheLastProgressLine(t *testing.T) {
	dir := t.TempDir()
	writeJob(t, dir, "6a2b3c4d", `{
		"detail": "all 41 tests pass; 2 fixes uncommitted (net formula + assertion)",
		"state": "done",
		"output": {"result": "Tests green: 9/9 suites, 41/41 assertions pass"},
		"sessionId": "6a2b3c4d-0000-4000-8000-000000000002",
		"resumeSessionId": "6a2b3c4d-0000-4000-8000-000000000002"
	}`)

	jobs, err := readJobs(dir)
	if err != nil {
		t.Fatalf("readJobs() error = %v", err)
	}
	got := jobs["6a2b3c4d-0000-4000-8000-000000000002"].detail()
	if got != "Tests green: 9/9 suites, 41/41 assertions pass" {
		t.Errorf("detail() = %q, want the closing summary", got)
	}
}

// An empty result is not a summary. Falling through to the progress line is
// what keeps a job that finished without writing one from showing a blank.
func TestReadJobsFallsBackWhenTheSummaryIsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeJob(t, dir, "j", `{"detail": "still the last thing said", "output": {"result": ""}, "sessionId": "s1"}`)

	jobs, _ := readJobs(dir)
	if got := jobs["s1"].detail(); got != "still the last thing said" {
		t.Errorf("detail() = %q, want the progress line", got)
	}
}

// A job resumes into a new transcript, and its name is an ai-title, so every
// transcript it ever occupied carries the same one. Keying on the current id is
// what marks exactly one of those rows as the job's own.
func TestReadJobsKeysOnTheTranscriptTheJobIsOnNow(t *testing.T) {
	dir := t.TempDir()
	writeJob(t, dir, "6a2b3c4d", `{
		"detail": "carrying on",
		"state": "working",
		"sessionId": "6a2b3c4d-0000-4000-8000-000000000002",
		"resumeSessionId": "7b3c4d5e-0000-4000-8000-000000000003"
	}`)

	jobs, err := readJobs(dir)
	if err != nil {
		t.Fatalf("readJobs() error = %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("readJobs() returned %d records, want 1: %v", len(jobs), jobs)
	}
	if _, ok := jobs["7b3c4d5e-0000-4000-8000-000000000003"]; !ok {
		t.Errorf("readJobs() = %v, want it keyed by resumeSessionId", jobs)
	}
	if _, ok := jobs["6a2b3c4d-0000-4000-8000-000000000002"]; ok {
		t.Error("the original transcript was also marked, which leaves two rows both claiming to be the job")
	}
}

func TestReadJobsFallsBackToSessionIDWhenThereIsNoResume(t *testing.T) {
	dir := t.TempDir()
	writeJob(t, dir, "j", `{"detail": "d", "sessionId": "only-id"}`)

	jobs, _ := readJobs(dir)
	if _, ok := jobs["only-id"]; !ok {
		t.Errorf("readJobs() = %v, want it keyed by sessionId", jobs)
	}
}

// A machine that has never run a background agent has no jobs directory, which
// is not a failure to report.
func TestReadJobsToleratesNoJobsDirectory(t *testing.T) {
	jobs, err := readJobs(t.TempDir())
	if err != nil {
		t.Errorf("readJobs() error = %v, want nil for a machine with no background agents", err)
	}
	if len(jobs) != 0 {
		t.Errorf("readJobs() = %v, want empty", jobs)
	}
}

// The jobs directory holds a pins.json beside the job directories, and a job
// mid-creation has a directory with no state.json yet. Neither is a record and
// neither is a failure.
func TestReadJobsSkipsNonRecords(t *testing.T) {
	dir := t.TempDir()
	writeJob(t, dir, "good", `{"detail": "d", "sessionId": "s1"}`)
	if err := os.WriteFile(filepath.Join(dir, "jobs", "pins.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "jobs", "half-made"), 0o755); err != nil {
		t.Fatal(err)
	}

	jobs, err := readJobs(dir)
	if err != nil {
		t.Fatalf("readJobs() error = %v, want nil", err)
	}
	if len(jobs) != 1 {
		t.Errorf("readJobs() returned %d records, want only the one real job: %v", len(jobs), jobs)
	}
}

// One corrupt record must cost its own row, never the rest: state.json is
// rewritten continuously while a job runs, so a torn read is normal traffic.
func TestReadJobsReportsACorruptRecordAndKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	writeJob(t, dir, "good", `{"detail": "d", "state": "working", "sessionId": "s1"}`)
	writeJob(t, dir, "torn", `{"detail": "half a`)

	jobs, err := readJobs(dir)
	if err == nil {
		t.Error("readJobs() error = nil, want the unreadable record reported")
	}
	if _, ok := jobs["s1"]; !ok {
		t.Errorf("readJobs() = %v, want the readable record kept alongside the failure", jobs)
	}
}

func TestApplyJobsMarksOnlyTheMatchingSession(t *testing.T) {
	sessions := []session.Session{
		{Agent: "claude", ID: "s1", Name: "from the transcript"},
		{Agent: "claude", ID: "s2", Name: "untouched"},
	}
	jobs := map[string]jobRecord{
		"s1": {Name: "from the job", Detail: "Verifying", State: "working", Tokens: 99},
	}

	applyJobs(sessions, jobs)

	if sessions[0].JobState != "working" || sessions[0].Detail != "Verifying" || sessions[0].Tokens != 99 {
		t.Errorf("sessions[0] = %+v, want the job's state, detail and tokens", sessions[0])
	}
	// The job's name and the transcript's ai-title are the same generated
	// string, so taking it can only overwrite a value with itself — and on a
	// transcript whose own title differs, it would be the wrong one.
	if sessions[0].Name != "from the transcript" {
		t.Errorf("Name = %q, want the transcript's own title kept", sessions[0].Name)
	}
	if sessions[1].JobState != "" || sessions[1].Detail != "" || sessions[1].Tokens != 0 {
		t.Errorf("sessions[1] = %+v, want it untouched", sessions[1])
	}
}

// A background agent runs under the daemon and never appears in the live
// registry, so its record is the only thing that can say what it is doing. It
// must survive the whole discovery path, not just readJobs.
func TestDiscoverFoldsJobRecordsOntoTheirSessions(t *testing.T) {
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "-Users-e-repo")
	writeTranscript(t, projects, "job-session.jsonl", `{"type":"user","cwd":"/Users/dev/repo","gitBranch":"main","message":{"role":"user","content":"start"}}`)
	writeJob(t, dir, "short", `{
		"detail": "Verifying all 5 fixed cases across 4 runs",
		"state": "working",
		"tokens": 43223,
		"sessionId": "job-session",
		"resumeSessionId": "job-session"
	}`)

	sessions, err := New(dir).Discover(t.Context(), 0)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("Discover() returned %d sessions, want 1", len(sessions))
	}
	if sessions[0].JobState != "working" {
		t.Errorf("JobState = %q, want %q", sessions[0].JobState, "working")
	}
	if sessions[0].Detail == "" {
		t.Error("Detail is empty, want the job's progress line")
	}
	if sessions[0].Tokens != 43223 {
		t.Errorf("Tokens = %d, want 43223", sessions[0].Tokens)
	}
}

// The job store is a handful of files against hundreds of transcripts. A
// failure there costs the detail column and nothing else.
func TestDiscoverSurvivesAnUnreadableJobStore(t *testing.T) {
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "-Users-e-repo")
	writeTranscript(t, projects, "s1.jsonl", `{"type":"user","cwd":"/Users/dev/repo","message":{"role":"user","content":"hello"}}`)
	writeJob(t, dir, "torn", `{"detail": "half a`)

	sessions, err := New(dir).Discover(t.Context(), 0)
	if err == nil {
		t.Error("Discover() error = nil, want the job failure reported")
	}
	if len(sessions) != 1 {
		t.Fatalf("Discover() returned %d sessions, want the transcript kept despite the job failure", len(sessions))
	}
	if sessions[0].Name == "" {
		t.Error("the surviving session has no name, so the job failure cost more than the detail column")
	}
}
