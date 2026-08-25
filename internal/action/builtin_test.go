package action

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

func find(t *testing.T, reg *Registry, s session.Session, id string) Action {
	t.Helper()
	for _, a := range reg.For(s) {
		if a.ID() == id {
			return a
		}
	}
	t.Fatalf("action %q not available for %+v", id, s)
	return nil
}

func available(reg *Registry, s session.Session, id string) bool {
	for _, a := range reg.For(s) {
		if a.ID() == id {
			return true
		}
	}
	return false
}

func TestCopyResumeCommandCopiesWhatTheAgentSupplies(t *testing.T) {
	var copied string
	reg := NewRegistry(Config{
		Clipboard: func(text string) error { copied = text; return nil },
		ResumeCommand: func(s session.Session) (string, error) {
			return "cd " + ShellQuote(s.Cwd) + " && codex resume " + ShellQuote(s.ID), nil
		},
	})
	s := session.Session{Agent: "codex", ID: "abc", Cwd: "/Users/x/My Project"}

	res, err := find(t, reg, s, "copy.resume").Run(context.Background(), s)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if want := `cd '/Users/x/My Project' && codex resume 'abc'`; copied != want {
		t.Errorf("clipboard = %q, want %q — the agent's own command, not a hardcoded claude one", copied, want)
	}
	if res.Status == "" {
		t.Error("Result.Status is empty; the footer would show nothing")
	}
	if res.Refresh {
		t.Error("Result.Refresh = true; copying does not change the list")
	}
}

// A resume command that cannot be derived must leave the entry out rather than
// offer a plausible command for the wrong agent, which would run and do
// something.
func TestCopyResumeUnavailableWithoutAConfiguredCommand(t *testing.T) {
	reg := NewRegistry(Config{Clipboard: func(string) error { return nil }})

	if available(reg, session.Session{Agent: "codex", ID: "abc", Cwd: "/repo"}, "copy.resume") {
		t.Error("copy.resume offered with no ResumeCommand configured")
	}
}

func TestCopyResumeReportsAFailingResumeCommand(t *testing.T) {
	failing := func(session.Session) (string, error) {
		return "", errors.New(`no provider registered for agent "ghost"`)
	}
	reg := NewRegistry(Config{Clipboard: func(string) error { return nil }, ResumeCommand: failing})
	s := session.Session{Agent: "ghost", ID: "abc"}

	if available(reg, s, "copy.resume") {
		t.Error("copy.resume offered for an agent whose resume command cannot be built")
	}

	// Built directly: an unavailable action never reaches the palette, so this is
	// the only way to reach Run and prove it reports the failure instead of
	// copying an empty string.
	act := copyAction{id: "copy.resume", write: func(string) error { return nil }, value: failing}
	if _, err := act.Run(context.Background(), s); err == nil {
		t.Error("Run() error = nil, want the resume-command failure surfaced")
	}
}

func TestShellQuoteSurvivesAShellRoundTrip(t *testing.T) {
	tests := []string{"My Project", "o'brien"}

	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("MkdirAll(%q) error = %v", dir, err)
			}

			out, err := exec.Command("sh", "-c", "cd "+ShellQuote(dir)+" && pwd").Output()
			if err != nil {
				t.Fatalf("shell round trip on %s error = %v", ShellQuote(dir), err)
			}
			if got := strings.TrimSpace(string(out)); got != dir {
				t.Errorf("cd %s && pwd = %q, want %q; a human pasting the copied command would land in the wrong place", ShellQuote(dir), got, dir)
			}
		})
	}
}

func TestCopyReportsValueWhenNoClipboardTool(t *testing.T) {
	reg := NewRegistry(Config{
		Clipboard: func(string) error { return errors.New("exec: \"pbcopy\": executable file not found in $PATH") },
	})
	s := session.Session{Agent: "claude", ID: "abc", Cwd: "/repo"}

	_, err := find(t, reg, s, "copy.id").Run(context.Background(), s)
	if err == nil {
		t.Fatal("Run() error = nil, want an error when no clipboard tool exists")
	}
	if !strings.Contains(err.Error(), "abc") {
		t.Errorf("error = %q, want it to carry the value so it is recoverable by hand", err)
	}
}

func TestCopyAvailabilityFollowsTheField(t *testing.T) {
	reg := NewRegistry(Config{Clipboard: func(string) error { return nil }})

	if available(reg, session.Session{Agent: "claude"}, "copy.transcript") {
		t.Error("copy.transcript offered for a session with no transcript path")
	}
	if !available(reg, session.Session{Agent: "claude", Transcript: "/t/a.jsonl"}, "copy.transcript") {
		t.Error("copy.transcript missing for a session that has one")
	}
}

func TestOpenTicket(t *testing.T) {
	var opened string
	reg := NewRegistry(Config{
		Workspace: "acme",
		Prefixes:  []string{"eng"},
		OpenURL:   func(url string) error { opened = url; return nil },
	})
	s := session.Session{Agent: "claude", ID: "a", GitBranch: "eng-3140-search ranking"}

	if _, err := find(t, reg, s, "ticket.open").Run(context.Background(), s); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if opened != "https://linear.app/acme/issue/ENG-3140" {
		t.Errorf("opened %q, want the Linear issue URL", opened)
	}
}

func TestOpenTicketUnavailableWithoutAKey(t *testing.T) {
	reg := NewRegistry(Config{Workspace: "acme", Prefixes: []string{"eng"}, OpenURL: func(string) error { return nil }})

	if available(reg, session.Session{Agent: "claude", GitBranch: "develop"}, "ticket.open") {
		t.Error("ticket.open offered for a branch with no key")
	}
}

func TestTicketActionExplainsWhatIsMissing(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		s    session.Session
		want string
	}{
		{
			name: "no workspace",
			cfg:  Config{Prefixes: []string{"eng"}},
			s:    session.Session{Agent: "claude", GitBranch: "eng-3140-x"},
			want: "--linear-workspace",
		},
		{
			name: "no prefixes",
			cfg:  Config{Workspace: "acme"},
			s:    session.Session{Agent: "claude", GitBranch: "eng-3140-x"},
			want: "--ticket-prefix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opened := false
			tt.cfg.OpenURL = func(string) error { opened = true; return nil }
			reg := NewRegistry(tt.cfg)

			act := find(t, reg, tt.s, "ticket.open")
			label := act.Label(tt.s)
			if !strings.Contains(label, "unavailable") || !strings.Contains(label, tt.want) {
				t.Errorf("Label() = %q, want it to read as unavailable and name %s", label, tt.want)
			}

			res, err := act.Run(context.Background(), tt.s)
			if err != nil {
				t.Fatalf("Run() error = %v, want the explanation in the footer instead", err)
			}
			if !strings.Contains(res.Status, tt.want) {
				t.Errorf("Result.Status = %q, want it to name %s", res.Status, tt.want)
			}
			if opened {
				t.Error("a browser was opened for a ticket that cannot be resolved")
			}
		})
	}
}

func TestTicketActionAbsentWhenNothingIsConfigured(t *testing.T) {
	reg := NewRegistry(Config{OpenURL: func(string) error { return nil }})

	if available(reg, session.Session{Agent: "claude", GitBranch: "eng-3140-x"}, "ticket.open") {
		t.Error("ticket.open offered with neither a workspace nor a prefix; nothing about it is configured")
	}
}

func TestKillProcess(t *testing.T) {
	var signalled, sig int
	started := time.Now().Add(-time.Hour)
	reg := NewRegistry(Config{
		Alive:   func(int) bool { return true },
		Signal:  func(pid int, s int) error { signalled, sig = pid, s; return nil },
		Elapsed: func(int) (time.Duration, error) { return time.Since(started), nil },
	})
	// StartedAt is not optional: the kill action refuses a pid it cannot confirm
	// still belongs to the process the registry described.
	s := session.Session{Agent: "claude", ID: "a", Live: true, PID: 4242, Name: "webapp-8e", StartedAt: started}

	action := find(t, reg, s, "process.kill")
	if action.Confirm(s) == "" {
		t.Error("Confirm() is empty; killing a process must ask first")
	}
	res, err := action.Run(context.Background(), s)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if signalled != 4242 {
		t.Errorf("signalled pid %d, want 4242", signalled)
	}
	if sig != int(syscallSIGTERM) {
		t.Errorf("signal = %d, want SIGTERM (%d)", sig, int(syscallSIGTERM))
	}
	if !res.Refresh {
		t.Error("Result.Refresh = false; the palette would not refresh after a kill")
	}
}

func TestKillRechecksLivenessBeforeSignalling(t *testing.T) {
	called := false
	reg := NewRegistry(Config{
		Alive:  func(int) bool { return false },
		Signal: func(int, int) error { called = true; return nil },
	})
	s := session.Session{Agent: "claude", ID: "a", Live: true, PID: 4242}

	// Available() is evaluated at palette-open time, so build the action directly
	// to prove Run itself re-checks rather than trusting the stale row.
	act := killAction{alive: func(int) bool { return false }, signal: func(int, int) error { called = true; return nil }}
	if _, err := act.Run(context.Background(), s); err == nil {
		t.Error("Run() error = nil, want a refusal when the pid is no longer alive")
	}
	if called {
		t.Error("signal was sent to a pid that is no longer alive")
	}
	if available(reg, s, "process.kill") {
		t.Error("process.kill offered for a pid that is not alive")
	}
}

func TestKillUnavailableForHistoryRows(t *testing.T) {
	reg := NewRegistry(Config{Alive: func(int) bool { return true }, Signal: func(int, int) error { return nil }})

	if available(reg, session.Session{Agent: "claude", PID: 1}, "process.kill") {
		t.Error("process.kill offered for a row that is not live")
	}
}

func TestActionLabels(t *testing.T) {
	reg := NewRegistry(Config{
		Workspace: "acme",
		Prefixes:  []string{"eng"},
		Clipboard: func(string) error { return nil },
		OpenURL:   func(string) error { return nil },
		Alive:     func(int) bool { return true },
		Signal:    func(int, int) error { return nil },
	})

	tests := []struct {
		name string
		id   string
		s    session.Session
		want string
	}{
		{
			name: "copy action",
			id:   "copy.id",
			s:    session.Session{Agent: "claude", ID: "abc"},
			want: "copy session id",
		},
		{
			name: "ticket action",
			id:   "ticket.open",
			s:    session.Session{Agent: "claude", GitBranch: "eng-3140-search ranking"},
			want: "open in Linear - Ticket ENG-3140",
		},
		{
			name: "kill action",
			id:   "process.kill",
			s:    session.Session{Agent: "claude", Live: true, PID: 4242},
			want: "kill process (pid 4242)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := find(t, reg, tt.s, tt.id).Label(tt.s); got != tt.want {
				t.Errorf("Label() = %q, want %q", got, tt.want)
			}
		})
	}
}

func mustEditorTool(name string) Tool {
	tool, _ := LookupEditor(name)
	return tool
}

func mustVCSTool(name string) Tool {
	tool, _ := LookupVCS(name)
	return tool
}

func mustEditor(t *testing.T, name string) Tool {
	t.Helper()
	tool, ok := LookupEditor(name)
	if !ok {
		t.Fatalf("LookupEditor(%q) is not in the table", name)
	}
	return tool
}

func allInstalled(string) (string, error)  { return "/usr/local/bin/x", nil }
func noneInstalled(string) (string, error) { return "", errors.New("not found") }

// Every invocation is that tool's own documented form, and the forms differ. A
// row that borrowed another's shape would be invisible at runtime: fork exits 0
// printing nothing whatever it is given.
func TestToolTablesUseEachToolsDocumentedInvocation(t *testing.T) {
	for _, tt := range []struct {
		name     string
		lookup   func(string) (Tool, bool)
		key      string
		binary   string
		wantArgs []string
	}{
		{"vscode", LookupEditor, "vscode", "code", []string{"/d"}},
		{"cursor", LookupEditor, "cursor", "cursor", []string{"/d"}},
		{"zed", LookupEditor, "zed", "zed", []string{"/d"}},
		{"sublime", LookupEditor, "sublime", "subl", []string{"/d"}},
		{"intellij", LookupEditor, "intellij", "idea", []string{"/d"}},
		{"fork", LookupVCS, "fork", "fork", []string{"-C", "/d", "open"}},
		{"sourcetree", LookupVCS, "sourcetree", "stree", []string{"/d"}},
		{"gitkraken", LookupVCS, "gitkraken", "gitkraken", []string{"--path", "/d"}},
		{"tower", LookupVCS, "tower", "gittower", []string{"/d"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tool, ok := tt.lookup(tt.key)
			if !ok {
				t.Fatalf("%s is not in the table", tt.key)
			}
			if tool.Binary != tt.binary {
				t.Errorf("Binary = %q, want %q", tool.Binary, tt.binary)
			}
			if got := strings.Join(tool.Args("/d"), "\x00"); got != strings.Join(tt.wantArgs, "\x00") {
				t.Errorf("Args = %v, want %v", tool.Args("/d"), tt.wantArgs)
			}
			if tool.Name == "" {
				t.Error("Name is empty, so the label would read \"open in \"")
			}
		})
	}

	if _, ok := LookupEditor("helix"); ok {
		t.Error("LookupEditor accepted a tool that is not in the table")
	}
	if _, ok := LookupVCS("magit"); ok {
		t.Error("LookupVCS accepted a tool that is not in the table")
	}
}

// Every accepted config spelling must reach a real row, or the error message
// offers a value that does not work.
func TestEveryAdvertisedToolNameResolves(t *testing.T) {
	for _, name := range EditorNames() {
		if _, ok := LookupEditor(name); !ok {
			t.Errorf("EditorNames() advertises %q but LookupEditor rejects it", name)
		}
	}
	for _, name := range VCSNames() {
		if _, ok := LookupVCS(name); !ok {
			t.Errorf("VCSNames() advertises %q but LookupVCS rejects it", name)
		}
	}
}

func TestLaunchActionRunsTheConfiguredToolsInvocation(t *testing.T) {
	// A real directory: the launch actions stat what they are handed, because the
	// value comes out of a transcript rather than from this process.
	dir := t.TempDir()

	for _, tt := range []struct {
		id       string
		tool     Tool
		binary   string
		wantArgs []string
	}{
		{"launch.editor", mustEditorTool("vscode"), "code", []string{dir}},
		{"launch.vcs", mustVCSTool("fork"), "fork", []string{"-C", dir, "open"}},
	} {
		t.Run(tt.id, func(t *testing.T) {
			var gotBinary string
			var gotArgs []string
			cfg := Config{
				LookPath: func(tool string) (string, error) { return "/usr/local/bin/" + tool, nil },
				Run: func(_ context.Context, binary string, args ...string) error {
					gotBinary, gotArgs = binary, args
					return nil
				},
			}
			if tt.id == "launch.editor" {
				cfg.Editor = tt.tool
			} else {
				cfg.VCS = tt.tool
			}

			s := session.Session{Cwd: dir}
			res, err := find(t, NewRegistry(cfg), s, tt.id).Run(context.Background(), s)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if gotBinary != "/usr/local/bin/"+tt.binary {
				t.Errorf("ran %q, want the resolved %s", gotBinary, tt.binary)
			}
			if strings.Join(gotArgs, "\x00") != strings.Join(tt.wantArgs, "\x00") {
				t.Errorf("args = %v, want %v", gotArgs, tt.wantArgs)
			}
			if res.Status == "" {
				t.Error("Result.Status is empty; silence in the footer reads as a dropped keypress")
			}
			if res.Refresh {
				t.Error("Result.Refresh = true, want no reload: opening an app changes no session")
			}
		})
	}
}

// A machine with no config must behave as the tool did before there was one.
func TestRegistryDetectsAToolWhenNoneIsConfigured(t *testing.T) {
	// Only the third editor in table order is installed.
	reg := NewRegistry(Config{
		LookPath: func(tool string) (string, error) {
			if tool == "zed" {
				return "/usr/local/bin/zed", nil
			}
			return "", errors.New("not found")
		},
		Run: func(context.Context, string, ...string) error { return nil },
	})

	s := session.Session{Cwd: "/Users/dev/git/x"}
	if label := find(t, reg, s, "launch.editor").Label(s); !strings.Contains(label, "Zed") {
		t.Errorf("Label() = %q, want the detected editor", label)
	}
}

// Detection order is the table order, so the same machine always resolves the
// same tool rather than whichever PATH lookup happened to be tried first.
func TestDetectionFollowsTableOrder(t *testing.T) {
	reg := NewRegistry(Config{
		LookPath: func(tool string) (string, error) {
			if tool == "code" || tool == "zed" {
				return "/usr/local/bin/" + tool, nil
			}
			return "", errors.New("not found")
		},
		Run: func(context.Context, string, ...string) error { return nil },
	})

	s := session.Session{Cwd: "/Users/dev/git/x"}
	if label := find(t, reg, s, "launch.editor").Label(s); !strings.Contains(label, "VS Code") {
		t.Errorf("Label() = %q, want the first installed editor in table order", label)
	}
}

// Detection runs once. Available is called on every frame the palette draws,
// and a PATH lookup there is the shape that already caused one action
// retargeting bug in this picker. This pins the injected lookup only: a
// hand-rolled exec.LookPath inside Available bypasses the seam.
func TestDetectionRunsOnceNotPerFrame(t *testing.T) {
	var lookups int
	reg := NewRegistry(Config{
		LookPath: func(tool string) (string, error) { lookups++; return "/usr/local/bin/" + tool, nil },
		Run:      func(context.Context, string, ...string) error { return nil },
	})

	built := lookups
	for range 5 {
		reg.For(session.Session{Cwd: "/Users/dev/git/x"})
	}
	if lookups != built {
		t.Errorf("LookPath ran %d more times after the registry was built, want 0", lookups-built)
	}
}

// An explicit choice that cannot be honoured is worth reporting; a failed guess
// is not.
func TestConfiguredToolMissingFromPathStillAppears(t *testing.T) {
	s := session.Session{Cwd: "/Users/dev/git/x"}

	configured := NewRegistry(Config{
		Editor:   mustEditor(t, "zed"),
		LookPath: noneInstalled,
		Run:      func(context.Context, string, ...string) error { return nil },
	})
	label := find(t, configured, s, "launch.editor").Label(s)
	if !strings.Contains(label, "not on PATH") || !strings.Contains(label, "Zed") {
		t.Errorf("Label() = %q, want it to name the missing configured tool", label)
	}
	if _, err := find(t, configured, s, "launch.editor").Run(context.Background(), s); err == nil {
		t.Error("Run() error = nil, want a refusal when the configured tool is missing")
	}

	detected := NewRegistry(Config{LookPath: noneInstalled, Run: func(context.Context, string, ...string) error { return nil }})
	for _, a := range detected.For(s) {
		if a.ID() == "launch.editor" || a.ID() == "launch.vcs" {
			t.Errorf("%s is offered when nothing was configured and nothing detected", a.ID())
		}
	}
}

// A session with no working directory has nothing to open.
func TestLaunchActionIsUnavailableWithoutAWorkingDirectory(t *testing.T) {
	reg := NewRegistry(Config{LookPath: allInstalled, Run: func(context.Context, string, ...string) error { return nil }})

	for _, a := range reg.For(session.Session{}) {
		if a.ID() == "launch.editor" || a.ID() == "launch.vcs" {
			t.Errorf("%s is offered for a session with no working directory", a.ID())
		}
	}
}

func TestLaunchActionReportsAFailedHandOff(t *testing.T) {
	reg := NewRegistry(Config{
		Editor:   mustEditor(t, "vscode"),
		LookPath: func(tool string) (string, error) { return "/usr/local/bin/" + tool, nil },
		Run:      func(context.Context, string, ...string) error { return errors.New("exit status 1") },
	})

	s := session.Session{Cwd: t.TempDir()}
	if _, err := find(t, reg, s, "launch.editor").Run(context.Background(), s); err == nil {
		t.Error("Run() error = nil, want the failed hand-off reported")
	}
}

func TestPullRequestActionResolvesTheBranchAndOpensTheURL(t *testing.T) {
	var gotDir, gotBinary string
	var gotArgs []string
	var opened string

	reg := NewRegistry(Config{
		LookPath: func(tool string) (string, error) { return "/opt/homebrew/bin/" + tool, nil },
		Output: func(_ context.Context, dir, binary string, args ...string) (string, error) {
			gotDir, gotBinary, gotArgs = dir, binary, args
			return `{"number":2250,"state":"MERGED","url":"https://github.com/acme/webapp/pull/2250"}`, nil
		},
		OpenURL: func(url string) error { opened = url; return nil },
	})

	s := session.Session{Cwd: t.TempDir(), GitBranch: "eng-3140-move-the-search ranking-endpoints"}
	pr := find(t, reg, s, "pr.open")
	res, err := pr.Run(context.Background(), s)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// gh works out the repository from the working directory, and the picker's
	// own is not the session's.
	if gotDir != s.Cwd {
		t.Errorf("ran in %q, want the session's directory %q", gotDir, s.Cwd)
	}
	if gotBinary != "/opt/homebrew/bin/gh" {
		t.Errorf("ran %q, want the resolved gh", gotBinary)
	}
	// The branch is named rather than left to gh's current-branch default: the
	// checkout may have moved on since the session ran.
	if !slices.Contains(gotArgs, s.GitBranch) {
		t.Errorf("args = %v, want the session's branch named explicitly", gotArgs)
	}
	if opened != "https://github.com/acme/webapp/pull/2250" {
		t.Errorf("opened %q, want the PR URL gh reported", opened)
	}
	if !strings.Contains(res.Status, "2250") {
		t.Errorf("Result.Status = %q, want it to name the PR", res.Status)
	}
}

// A branch with no pull request is the common case, and gh's own message says
// so. Swallowing it would leave the footer claiming something opened.
func TestPullRequestActionReportsWhenThereIsNoPR(t *testing.T) {
	opened := false
	reg := NewRegistry(Config{
		LookPath: func(tool string) (string, error) { return "/opt/homebrew/bin/" + tool, nil },
		Output: func(context.Context, string, string, ...string) (string, error) {
			return "", errors.New("no pull requests found for branch \"wip\"")
		},
		OpenURL: func(string) error { opened = true; return nil },
	})

	s := session.Session{Cwd: t.TempDir(), GitBranch: "wip"}
	_, err := find(t, reg, s, "pr.open").Run(context.Background(), s)
	if err == nil {
		t.Fatal("Run() error = nil, want the missing pull request reported")
	}
	if !strings.Contains(err.Error(), "no pull requests found") {
		t.Errorf("Run() error = %v, want gh's own diagnostic carried through", err)
	}
	if opened {
		t.Error("a browser was opened despite there being no pull request")
	}
}

// Whether a branch has a pull request is a network round trip, and Available
// runs on every frame the palette draws.
func TestPullRequestActionAvailabilityTouchesNoNetwork(t *testing.T) {
	reg := NewRegistry(Config{
		LookPath: func(tool string) (string, error) { return "/opt/homebrew/bin/" + tool, nil },
		Output: func(context.Context, string, string, ...string) (string, error) {
			t.Fatal("Output was called while deciding availability")
			return "", nil
		},
	})

	for range 5 {
		find(t, reg, session.Session{Cwd: "/Users/dev/git/x", GitBranch: "feature"}, "pr.open")
	}
}

func TestPullRequestActionNeedsABranchAndADirectory(t *testing.T) {
	reg := NewRegistry(Config{
		LookPath: func(tool string) (string, error) { return "/opt/homebrew/bin/" + tool, nil },
		Output:   func(context.Context, string, string, ...string) (string, error) { return "", nil },
	})

	for _, tt := range []struct {
		name string
		s    session.Session
	}{
		{"no branch", session.Session{Cwd: "/Users/dev/git/x"}},
		{"no directory", session.Session{GitBranch: "feature"}},
		// A detached head is what the transcript records for a worktree checked
		// out at a commit; there is no branch to ask GitHub about.
		{"detached head", session.Session{Cwd: "/Users/dev/git/x", GitBranch: "HEAD"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if available(reg, tt.s, "pr.open") {
				t.Errorf("pr.open is offered for %+v", tt.s)
			}
		})
	}
}

func TestPullRequestActionRejectsOutputItCannotRead(t *testing.T) {
	for _, tt := range []struct {
		name string
		out  string
	}{
		{"not json", "something went sideways"},
		{"no url", `{"number":7,"state":"OPEN"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			opened := false
			reg := NewRegistry(Config{
				LookPath: func(tool string) (string, error) { return "/opt/homebrew/bin/" + tool, nil },
				Output: func(context.Context, string, string, ...string) (string, error) {
					return tt.out, nil
				},
				OpenURL: func(string) error { opened = true; return nil },
			})

			s := session.Session{Cwd: "/Users/dev/git/x", GitBranch: "feature"}
			if _, err := find(t, reg, s, "pr.open").Run(context.Background(), s); err == nil {
				t.Error("Run() error = nil, want unreadable gh output reported")
			}
			if opened {
				t.Error("a browser was opened on output that carried no URL")
			}
		})
	}
}

// SystemOutput must run in the directory it is given, or gh resolves the wrong
// repository — and it must carry the tool's diagnostic, since ExitError alone
// says only "exit status 1".
func TestSystemOutputRunsInTheGivenDirectoryAndCarriesStderr(t *testing.T) {
	dir := t.TempDir()

	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh available")
	}

	out, err := SystemOutput(context.Background(), dir, sh, "-c", "pwd")
	if err != nil {
		t.Fatalf("SystemOutput() error = %v", err)
	}
	if !strings.Contains(out, filepath.Base(dir)) {
		t.Errorf("SystemOutput() ran in %q, want %q", strings.TrimSpace(out), dir)
	}

	_, err = SystemOutput(context.Background(), dir, sh, "-c", "echo the real reason >&2; exit 1")
	if err == nil {
		t.Fatal("SystemOutput() error = nil, want the failure reported")
	}
	if !strings.Contains(err.Error(), "the real reason") {
		t.Errorf("SystemOutput() error = %v, want the tool's own diagnostic", err)
	}
}

func TestLinearIsTheProviderThatShips(t *testing.T) {
	p, ok := LookupProvider("linear")
	if !ok {
		t.Fatal("linear is not in the provider table")
	}
	if got := p.IssueURL("acme", "ENG-3170"); got != "https://linear.app/acme/issue/ENG-3170" {
		t.Errorf("IssueURL() = %q", got)
	}
	// A half-configured tool has no URL, and saying so is what keeps the action
	// reporting the missing flag instead of opening a broken address.
	if got := p.IssueURL("", "ENG-3170"); got != "" {
		t.Errorf("IssueURL() = %q, want empty without a workspace", got)
	}
	if got := p.IssueURL("acme", ""); got != "" {
		t.Errorf("IssueURL() = %q, want empty without a key", got)
	}

	if _, ok := LookupProvider("jira"); ok {
		t.Error("jira resolved, but only linear ships")
	}
	for _, name := range ProviderNames() {
		if _, ok := LookupProvider(name); !ok {
			t.Errorf("ProviderNames() advertises %q but LookupProvider rejects it", name)
		}
	}
}

// An unset provider must not drop the entry: the flags this replaces defaulted
// to Linear, so a config file that omits the key keeps working.
func TestTicketActionDefaultsToLinear(t *testing.T) {
	var opened string
	reg := NewRegistry(Config{
		Workspace: "acme",
		Prefixes:  []string{"eng"},
		OpenURL:   func(u string) error { opened = u; return nil },
	})

	s := session.Session{Agent: "claude", GitBranch: "eng-3170-stale-cache"}
	if _, err := find(t, reg, s, "ticket.open").Run(context.Background(), s); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if opened != "https://linear.app/acme/issue/ENG-3170" {
		t.Errorf("opened %q", opened)
	}
}

// The configured provider is what renders the URL, not a hardcoded shape.
func TestTicketActionUsesTheConfiguredProvider(t *testing.T) {
	var opened string
	reg := NewRegistry(Config{
		Provider:  Provider{Name: "stub", IssueURL: func(workspace, key string) string { return "https://tracker/" + workspace + "/" + key }},
		Workspace: "team",
		Prefixes:  []string{"eng"},
		OpenURL:   func(u string) error { opened = u; return nil },
	})

	s := session.Session{Agent: "claude", GitBranch: "eng-3170-stale-cache"}
	if _, err := find(t, reg, s, "ticket.open").Run(context.Background(), s); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if opened != "https://tracker/team/ENG-3170" {
		t.Errorf("opened %q, want the configured provider's shape", opened)
	}
}

func TestSessionDirRefusesADirectoryThatIsNotOne(t *testing.T) {
	real := t.TempDir()
	file := filepath.Join(real, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name    string
		dir     string
		wantErr bool
	}{
		{"an existing directory", real, false},
		{"empty", "", true},
		{"a relative path", "relative/dir", true},
		{"a flag", "--json=/etc/passwd", true},
		{"a path that does not exist", filepath.Join(real, "nope"), true},
		{"a file", file, true},
		{"an escape in the path", real + "\x1b]52;c;x\x07", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := sessionDir(tt.dir); (err != nil) != tt.wantErr {
				t.Errorf("sessionDir(%q) = %v, wantErr %v", tt.dir, err, tt.wantErr)
			}
		})
	}
}

// The branch is recorded in the transcript, so it arrives as whatever wrote the
// transcript put there, and gh takes it positionally.
func TestPullRequestRunSeparatesTheBranchAndRefusesAFlag(t *testing.T) {
	dir := t.TempDir()

	var got []string
	output := func(_ context.Context, _, _ string, args ...string) (string, error) {
		got = args
		return `{"url":"https://github.com/o/r/pull/7","number":7,"state":"OPEN"}`, nil
	}

	var opened string
	a := pullRequestAction{binary: "/usr/bin/gh", output: output, open: func(u string) error { opened = u; return nil }}

	if _, err := a.Run(context.Background(), session.Session{Cwd: dir, GitBranch: "eng-3160"}); err != nil {
		t.Fatalf("Run() on a good branch error = %v", err)
	}
	if opened != "https://github.com/o/r/pull/7" {
		t.Errorf("opened %q, want the pull request URL", opened)
	}
	dash, branch := slices.Index(got, "--"), slices.Index(got, "eng-3160")
	if dash < 0 || branch < dash {
		t.Errorf("gh args %q do not put the branch after a -- separator", got)
	}

	if _, err := a.Run(context.Background(), session.Session{Cwd: dir, GitBranch: "--json=/etc/passwd"}); err == nil {
		t.Error("Run() accepted a flag-shaped branch")
	}
	if _, err := a.Run(context.Background(), session.Session{Cwd: filepath.Join(dir, "gone"), GitBranch: "main"}); err == nil {
		t.Error("Run() accepted a working directory that does not exist")
	}
}

// The platform opener is not a browser: given a path it opens an application,
// and given a custom scheme it hands the value to whichever app registered for
// it. The URL arrives from gh's output, which gh resolved by running git in a
// directory a transcript named.
func TestSystemOpenRefusesAnythingButHTTP(t *testing.T) {
	for _, url := range []string{
		"file:///Applications/Calculator.app",
		"/Applications/Calculator.app",
		"x-evil://run",
		"",
		"-a",
	} {
		if err := SystemOpen(url); err == nil {
			t.Errorf("SystemOpen(%q) returned no error", url)
		}
	}
}

func TestLaunchRunRefusesADirectoryThatIsNotOne(t *testing.T) {
	a := launchAction{
		id:     "launch.editor",
		tool:   Tool{Name: "VS Code", Binary: "code", Args: bareDir},
		binary: "/usr/local/bin/code",
		run:    func(context.Context, string, ...string) error { return nil },
	}

	if _, err := a.Run(context.Background(), session.Session{Cwd: t.TempDir()}); err != nil {
		t.Fatalf("Run() on a real directory error = %v", err)
	}
	if _, err := a.Run(context.Background(), session.Session{Cwd: "--goto=/etc/passwd"}); err == nil {
		t.Error("Run() accepted a flag-shaped working directory")
	}
}

// ps reports elapsed time as [[dd-]hh:]mm:ss on both macOS and Linux. The
// numeric form is why it is preferred to lstart, whose layout follows the
// locale; macOS ps has no etimes keyword at all.
func TestParseElapsedReadsEveryFormPsPrints(t *testing.T) {
	for _, tt := range []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{"seconds", "00:04", 4 * time.Second, false},
		{"minutes and seconds", "01:30", 90 * time.Second, false},
		{"ten minutes", "10:00", 10 * time.Minute, false},
		{"hours", "1:02:03", time.Hour + 2*time.Minute + 3*time.Second, false},
		{"padded hours", "01:02:03", time.Hour + 2*time.Minute + 3*time.Second, false},
		{"days", "28-22:33:26", 28*24*time.Hour + 22*time.Hour + 33*time.Minute + 26*time.Second, false},
		{"surrounding space", "  00:07  ", 7 * time.Second, false},
		{"empty", "", 0, true},
		{"not a time", "abc", 0, true},
		{"too many fields", "1:2:3:4", 0, true},
		{"a bare number", "42", 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseElapsed(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseElapsed(%q) = %v, %v; wantErr %v", tt.value, got, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("parseElapsed(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// A pid outlives the process that held it. The registry file supplies both the
// pid and the name drawn beside it, so a stale record — or a planted one — would
// otherwise aim the confirmation at one process and the SIGTERM at another.
func TestKillRefusesAPidWhoseStartTimeDisagreesWithTheRecord(t *testing.T) {
	started := time.Now().Add(-2 * time.Hour)

	for _, tt := range []struct {
		name       string
		startedAt  time.Time
		elapsed    time.Duration
		elapsedErr error
		wantSignal bool
	}{
		{"the pid started when the record says", started, 2 * time.Hour, nil, true},
		{"a second of drift is the same process", started, 2*time.Hour + time.Second, nil, true},
		{"the pid is far younger than the record", started, 30 * time.Second, nil, false},
		{"the pid is far older than the record", started, 40 * time.Hour, nil, false},
		{"the record carries no start time", time.Time{}, 2 * time.Hour, nil, false},
		{"ps could not be asked", started, 0, errors.New("ps failed"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var signalled int
			a := killAction{
				alive:   func(int) bool { return true },
				signal:  func(pid, _ int) error { signalled = pid; return nil },
				elapsed: func(int) (time.Duration, error) { return tt.elapsed, tt.elapsedErr },
			}

			s := session.Session{Name: "a session", Live: true, PID: 4242, StartedAt: tt.startedAt}
			_, err := a.Run(context.Background(), s)

			if tt.wantSignal {
				if err != nil {
					t.Fatalf("Run() error = %v, want a signal sent", err)
				}
				if signalled != 4242 {
					t.Errorf("signalled pid %d, want 4242", signalled)
				}
				return
			}
			if err == nil {
				t.Fatal("Run() sent a signal to a pid it could not confirm")
			}
			if signalled != 0 {
				t.Errorf("signalled pid %d after refusing", signalled)
			}
		})
	}
}

// The refusal has to be readable in a footer, so it says which of the two
// disagreed rather than only that they did.
func TestKillNamesWhyItRefused(t *testing.T) {
	a := killAction{
		alive:   func(int) bool { return true },
		signal:  func(int, int) error { return nil },
		elapsed: func(int) (time.Duration, error) { return time.Minute, nil },
	}

	_, err := a.Run(context.Background(), session.Session{Name: "s", PID: 7, StartedAt: time.Now().Add(-9 * time.Hour)})
	if err == nil {
		t.Fatal("Run() error = nil")
	}
	if !strings.Contains(err.Error(), "7") {
		t.Errorf("Run() error = %q, want the pid named", err)
	}
}
