package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
	"github.com/eduardvoiculescu/agent-sessions/internal/ticket"
	"github.com/eduardvoiculescu/agent-sessions/internal/untrusted"
)

const syscallSIGTERM = syscall.SIGTERM

func builtins(cfg Config) []Action {
	resume := cfg.ResumeCommand
	if resume == nil {
		resume = func(session.Session) (string, error) {
			return "", errors.New("no resume command is configured")
		}
	}

	return []Action{
		ticketAction{provider: cfg.Provider, workspace: cfg.Workspace, prefixes: cfg.Prefixes, open: cfg.OpenURL},
		copyAction{id: "copy.resume", label: "copy resume command", write: cfg.Clipboard, value: resume},
		copyAction{id: "copy.id", label: "copy session id", write: cfg.Clipboard, value: field(func(s session.Session) string { return s.ID })},
		copyAction{id: "copy.transcript", label: "copy transcript path", write: cfg.Clipboard, value: field(func(s session.Session) string { return s.Transcript })},
		copyAction{id: "copy.cwd", label: "copy working directory", write: cfg.Clipboard, value: field(func(s session.Session) string { return s.Cwd })},
		newPullRequestAction(cfg),
		newLaunchAction(cfg, "launch.vcs", cfg.VCS, VCSClients()),
		newLaunchAction(cfg, "launch.editor", cfg.Editor, Editors()),
		killAction{alive: cfg.Alive, signal: cfg.Signal, elapsed: cfg.Elapsed},
	}
}

// sessionDir validates a working directory read out of a transcript. Every tool
// launched on one either runs inside it or resolves a repository from it, so a
// value that is not an absolute path to an existing directory has to fail here
// rather than reach a subprocess.
func sessionDir(dir string) (string, error) {
	if err := untrusted.ArgValue(dir); err != nil {
		return "", fmt.Errorf("the session's directory is not usable: %w", err)
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("the session's directory %q is not an absolute path", dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("the session's directory %q cannot be read: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("the session's directory %q is not a directory", dir)
	}
	return dir, nil
}

// pullRequestAction opens the pull request a session's branch belongs to.
// Nothing on disk records it: Claude Code stores only the branch name, so the
// pull request is resolved by asking gh, the same way Claude Code does.
type pullRequestAction struct {
	binary string
	output func(ctx context.Context, dir, binary string, args ...string) (string, error)
	open   func(url string) error
}

func newPullRequestAction(cfg Config) pullRequestAction {
	binary, _ := cfg.LookPath("gh")
	return pullRequestAction{binary: binary, output: cfg.Output, open: cfg.OpenURL}
}

func (a pullRequestAction) ID() string                     { return "pr.open" }
func (a pullRequestAction) Group() Group                   { return GroupGoTo }
func (a pullRequestAction) Confirm(session.Session) string { return "" }

func (a pullRequestAction) Label(session.Session) string {
	if a.binary == "" {
		return "open PR on GitHub — unavailable: gh is not on PATH"
	}
	return "open PR on GitHub"
}

// Available asks nothing of the network. Whether a branch actually has a pull
// request is a round trip, and Available runs on every frame the palette draws,
// so the question is deferred to Run and answered in the footer.
func (a pullRequestAction) Available(s session.Session) bool {
	return s.Cwd != "" && s.GitBranch != "" && s.GitBranch != "HEAD"
}

func (a pullRequestAction) Run(ctx context.Context, s session.Session) (Result, error) {
	if a.binary == "" {
		return Result{}, errors.New("gh is not on PATH; install the GitHub CLI")
	}

	dir, err := sessionDir(s.Cwd)
	if err != nil {
		return Result{}, err
	}
	if err := untrusted.ArgValue(s.GitBranch); err != nil {
		return Result{}, fmt.Errorf("refusing to ask gh about this branch: %w", err)
	}

	// The branch is named explicitly rather than left to gh's own "current
	// branch" default: it is the branch the session worked on, recorded in its
	// transcript, and the checkout may have moved on since. It sits after the
	// separator because gh takes it positionally.
	out, err := a.output(ctx, dir, a.binary, "pr", "view", "--json", "url,number,state", "--", s.GitBranch)
	if err != nil {
		return Result{}, fmt.Errorf("no pull request for %s: %w", s.GitBranch, err)
	}

	var pr struct {
		URL    string `json:"url"`
		Number int    `json:"number"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal([]byte(out), &pr); err != nil {
		return Result{}, fmt.Errorf("reading gh output: %w", err)
	}
	if pr.URL == "" {
		return Result{}, fmt.Errorf("gh found no pull request URL for %s", s.GitBranch)
	}

	if err := a.open(pr.URL); err != nil {
		return Result{}, fmt.Errorf("opening %s: %w", pr.URL, err)
	}
	return Result{Status: fmt.Sprintf("opened PR #%d (%s)", pr.Number, strings.ToLower(pr.State))}, nil
}

// SystemOutput runs a tool in dir and returns its standard output. A failure
// carries the tool's own diagnostic: gh says "no pull requests found for branch
// X", and ExitError alone would say only "exit status 1".
func SystemOutput(ctx context.Context, dir, binary string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = gitSafeEnv()

	out, err := cmd.Output()
	if err == nil {
		return string(out), nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if msg := strings.TrimSpace(string(exitErr.Stderr)); msg != "" {
			return "", errors.New(msg)
		}
	}
	return "", err
}

// launchAction opens an external application on the session's working
// directory.
// Tool is one launchable application: how it is named to the owner, the binary
// looked up on PATH, and how it is invoked on a directory.
type Tool struct {
	Name   string
	Binary string
	Args   func(dir string) []string
}

// Editors and VCSClients are the known tools, in the order detection tries them
// when nothing is configured.
//
// Every invocation is that tool's own documented form, and the forms differ:
// `fork`'s usage is `fork [<options>] <command>`, so a bare path is read as a
// command name — and it exits 0 printing nothing whatever it is given, which is
// exactly the shape a wrong invocation hides behind. `code`'s usage is
// `code [options] [paths...]`, where a bare path is correct. Only Fork's and VS
// Code's have been verified against the real binaries.
func Editors() []Tool {
	return []Tool{
		{Name: "VS Code", Binary: "code", Args: bareDir},
		{Name: "Cursor", Binary: "cursor", Args: bareDir},
		{Name: "Zed", Binary: "zed", Args: bareDir},
		{Name: "Sublime Text", Binary: "subl", Args: bareDir},
		{Name: "IntelliJ IDEA", Binary: "idea", Args: bareDir},
	}
}

func VCSClients() []Tool {
	return []Tool{
		{Name: "Fork", Binary: "fork", Args: func(dir string) []string { return []string{"-C", dir, "open"} }},
		{Name: "Sourcetree", Binary: "stree", Args: bareDir},
		{Name: "GitKraken", Binary: "gitkraken", Args: func(dir string) []string { return []string{"--path", dir} }},
		{Name: "Tower", Binary: "gittower", Args: bareDir},
	}
}

func bareDir(dir string) []string { return []string{dir} }

// editorNames and vcsNames are the config-file spellings, kept beside the
// tables they select from so a new row cannot be reachable from one and not the
// other.
var (
	editorNames = map[string]string{"vscode": "code", "cursor": "cursor", "zed": "zed", "sublime": "subl", "intellij": "idea"}
	vcsNames    = map[string]string{"fork": "fork", "sourcetree": "stree", "gitkraken": "gitkraken", "tower": "gittower"}
)

func LookupEditor(name string) (Tool, bool) { return lookupTool(name, editorNames, Editors()) }
func LookupVCS(name string) (Tool, bool)    { return lookupTool(name, vcsNames, VCSClients()) }

func lookupTool(name string, names map[string]string, tools []Tool) (Tool, bool) {
	binary, ok := names[name]
	if !ok {
		return Tool{}, false
	}
	for _, tool := range tools {
		if tool.Binary == binary {
			return tool, true
		}
	}
	return Tool{}, false
}

// EditorNames and VCSNames are the accepted config values, sorted, for the
// error message that greets a misspelling.
func EditorNames() []string { return sortedKeys(editorNames) }
func VCSNames() []string    { return sortedKeys(vcsNames) }

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

type launchAction struct {
	id   string
	tool Tool
	// binary is the resolved path, or "" when the tool is not installed. It is
	// resolved once, when the registry is built: Available runs on every frame
	// the palette draws, and a PATH lookup there is the shape that already cost
	// this picker one action-retargeting bug.
	binary string
	// chosen marks a tool the owner named rather than one detection guessed at.
	// An explicit choice that cannot be honoured is worth reporting; a failed
	// guess is not, so only the former leaves an entry behind.
	chosen bool
	run    func(ctx context.Context, binary string, args ...string) error
}

// newLaunchAction resolves the category's tool: the configured one if there is
// one, otherwise the first in table order that is actually installed.
func newLaunchAction(cfg Config, id string, configured Tool, table []Tool) launchAction {
	if configured.Binary != "" {
		binary, _ := cfg.LookPath(configured.Binary)
		return launchAction{id: id, tool: configured, binary: binary, chosen: true, run: cfg.Run}
	}

	for _, tool := range table {
		if binary, err := cfg.LookPath(tool.Binary); err == nil && binary != "" {
			return launchAction{id: id, tool: tool, binary: binary, run: cfg.Run}
		}
	}
	return launchAction{id: id, run: cfg.Run}
}

func (a launchAction) ID() string                     { return a.id }
func (a launchAction) Group() Group                   { return GroupGoTo }
func (a launchAction) Confirm(session.Session) string { return "" }

func (a launchAction) Label(session.Session) string {
	label := "open in " + a.tool.Name
	if a.binary == "" {
		return label + " — unavailable: " + a.tool.Binary + " is not on PATH"
	}
	return label
}

// Available drops the entry only when nothing resolved at all. A configured
// tool that is missing keeps its entry so there is something to correct, the
// same way the ticket action reports an unset flag.
func (a launchAction) Available(s session.Session) bool {
	return s.Cwd != "" && a.tool.Binary != ""
}

func (a launchAction) Run(ctx context.Context, s session.Session) (Result, error) {
	if a.binary == "" {
		return Result{}, fmt.Errorf("%s is not on PATH; install %s's command line helper", a.tool.Binary, a.tool.Name)
	}
	dir, err := sessionDir(s.Cwd)
	if err != nil {
		return Result{}, err
	}
	if err := a.run(ctx, a.binary, a.tool.Args(dir)...); err != nil {
		return Result{}, fmt.Errorf("opening %s in %s: %w", dir, a.tool.Name, err)
	}
	return Result{Status: "opened in " + a.tool.Name}, nil
}

// SystemRun runs an external tool and waits for it. The CLI helpers this
// launches hand off to a GUI app and return at once, so waiting costs nothing
// and is what surfaces a hand-off that failed.
func SystemRun(ctx context.Context, binary string, args ...string) error {
	return exec.CommandContext(ctx, binary, args...).Run()
}

// field adapts a plain session field to the value signature copy.resume needs;
// only that one can fail, because only that one has to ask an agent what
// re-entering a session takes.
func field(get func(session.Session) string) func(session.Session) (string, error) {
	return func(s session.Session) (string, error) { return get(s), nil }
}

// ShellQuote makes a value safe to paste into a shell. Directory names may
// contain spaces, and a semicolon or $() in one would otherwise turn a copied
// command into several. Exported for the wiring layer, which assembles the
// resume command from a provider's argv.
func ShellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

type copyAction struct {
	id    string
	label string
	write func(string) error
	value func(session.Session) (string, error)
}

func (a copyAction) ID() string                     { return a.id }
func (a copyAction) Group() Group                   { return GroupClipboard }
func (a copyAction) Label(session.Session) string   { return a.label }
func (a copyAction) Confirm(session.Session) string { return "" }

func (a copyAction) Available(s session.Session) bool {
	value, err := a.value(s)
	return err == nil && value != ""
}

func (a copyAction) Run(_ context.Context, s session.Session) (Result, error) {
	value, err := a.value(s)
	if err != nil {
		return Result{}, fmt.Errorf("building the value to copy: %w", err)
	}
	if err := a.write(value); err != nil {
		return Result{}, fmt.Errorf("copying %q: %w", value, err)
	}
	return Result{Status: "copied " + value}, nil
}

// Provider is one tracker's URL shape. Only the shape is configurable data —
// never a command — so a self-hosted tracker naming its own base URL costs
// nothing in exposure.
type Provider struct {
	Name string
	// IssueURL renders the issue's address, or "" when it cannot: an empty
	// workspace or key is a half-configured tool, not a URL.
	IssueURL func(workspace, key string) string
}

// Providers is every known tracker. Only Linear ships; the table exists so that
// Jira and GitHub Issues are each a row plus a test, and so the shape of that
// addition is settled now rather than under pressure later.
func Providers() []Provider {
	return []Provider{
		{Name: "linear", IssueURL: ticket.IssueURL},
	}
}

func LookupProvider(name string) (Provider, bool) {
	for _, p := range Providers() {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

// ProviderNames is the accepted config values, sorted, for the error message
// that greets a misspelling.
func ProviderNames() []string {
	out := make([]string, 0, len(Providers()))
	for _, p := range Providers() {
		out = append(out, p.Name)
	}
	slices.Sort(out)
	return out
}

type ticketAction struct {
	provider  Provider
	workspace string
	prefixes  []string
	open      func(string) error
}

func (a ticketAction) ID() string                     { return "ticket.open" }
func (a ticketAction) Group() Group                   { return GroupGoTo }
func (a ticketAction) Confirm(session.Session) string { return "" }

func (a ticketAction) key(s session.Session) string {
	return ticket.FromBranch(s.GitBranch, a.prefixes)
}

// missing names the flag that stands between this session and its ticket, or ""
// when nothing does. Half-configured is easy to reach while the flags must be
// retyped every run, and an entry that silently vanished would leave the user
// with nothing to correct.
func (a ticketAction) missing(s session.Session) string {
	switch {
	case a.workspace == "" && a.key(s) != "":
		return "--linear-workspace is not set"
	case a.workspace != "" && len(a.prefixes) == 0 && s.GitBranch != "":
		return "--ticket-prefix is not set, so no issue key can be read from " + s.GitBranch
	}
	return ""
}

func (a ticketAction) Label(s session.Session) string {
	// The key is appended only when there is one: the entry is still offered
	// with a reason when the workspace or the prefix list is unset, and there is
	// no key to name in that case.
	label := "open in Linear"
	if key := a.key(s); key != "" {
		label += " - Ticket " + key
	}
	if reason := a.missing(s); reason != "" {
		return label + " — unavailable: " + reason
	}
	return label
}

func (a ticketAction) Available(s session.Session) bool {
	return a.provider.IssueURL(a.workspace, a.key(s)) != "" || a.missing(s) != ""
}

func (a ticketAction) Run(_ context.Context, s session.Session) (Result, error) {
	if reason := a.missing(s); reason != "" {
		return Result{Status: "cannot open a ticket: " + reason}, nil
	}

	url := a.provider.IssueURL(a.workspace, a.key(s))
	if err := a.open(url); err != nil {
		return Result{}, fmt.Errorf("opening %s: %w", url, err)
	}
	return Result{Status: "opened " + url}, nil
}

// pidDrift is how far the registry's stamp may sit from the pid's own start time
// and still be the same process. The registry writes its stamp once the process
// is up and ps reports whole seconds, so the two never agree exactly.
const pidDrift = 30 * time.Second

type killAction struct {
	alive   func(int) bool
	signal  func(pid int, sig int) error
	elapsed func(pid int) (time.Duration, error)
}

// sameProcess confirms the pid still belongs to the process the record
// describes. It refuses when it cannot tell: a record with no start time cannot
// be checked, and a planted one would simply omit the field to slip through a
// check that let the unknown case pass.
func (a killAction) sameProcess(s session.Session) error {
	if s.StartedAt.IsZero() {
		return fmt.Errorf("%s's record carries no start time, so pid %d cannot be confirmed as the same process", s.Name, s.PID)
	}

	elapsed, err := a.elapsed(s.PID)
	if err != nil {
		return fmt.Errorf("confirming pid %d: %w", s.PID, err)
	}

	drift := time.Since(s.StartedAt) - elapsed
	if drift < 0 {
		drift = -drift
	}
	if drift > pidDrift {
		return fmt.Errorf("pid %d has been running %s but %s's record is %s old; the pid belongs to another process now",
			s.PID, elapsed.Round(time.Second), s.Name, time.Since(s.StartedAt).Round(time.Second))
	}
	return nil
}

func (a killAction) ID() string   { return "process.kill" }
func (a killAction) Group() Group { return GroupDanger }

func (a killAction) Label(s session.Session) string {
	return fmt.Sprintf("kill process (pid %d)", s.PID)
}

func (a killAction) Available(s session.Session) bool {
	return s.Live && s.PID > 0 && a.alive(s.PID)
}

func (a killAction) Confirm(s session.Session) string {
	return fmt.Sprintf("Send SIGTERM to %s (pid %d)?", s.Name, s.PID)
}

func (a killAction) Run(_ context.Context, s session.Session) (Result, error) {
	// Repeated rather than trusted from Available, because the row on screen can
	// be a tick old and the process may have exited since.
	if !a.alive(s.PID) {
		return Result{}, fmt.Errorf("pid %d is no longer running", s.PID)
	}
	if err := a.sameProcess(s); err != nil {
		return Result{}, err
	}
	if err := a.signal(s.PID, int(syscallSIGTERM)); err != nil {
		return Result{}, fmt.Errorf("signalling pid %d: %w", s.PID, err)
	}
	return Result{Status: fmt.Sprintf("sent SIGTERM to pid %d", s.PID), Refresh: true}, nil
}

func defaultAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func defaultSignal(pid int, sig int) error {
	return syscall.Kill(pid, syscall.Signal(sig))
}

// SystemElapsed reports how long the process holding pid has been running.
// `ps -o etime=` rather than lstart, whose layout follows the locale, and rather
// than etimes, which is a procps keyword that macOS ps does not have.
func SystemElapsed(pid int) (time.Duration, error) {
	out, err := exec.Command("ps", "-o", "etime=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, fmt.Errorf("asking ps for the age of pid %d: %w", pid, err)
	}
	elapsed, err := parseElapsed(string(out))
	if err != nil {
		return 0, fmt.Errorf("reading the age of pid %d: %w", pid, err)
	}
	return elapsed, nil
}

// parseElapsed reads ps's elapsed-time column, whose form is [[dd-]hh:]mm:ss.
// Every field is numeric, which is the reason this column is preferred to
// lstart: the same string parses the same way under every locale.
func parseElapsed(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New("ps printed no elapsed time")
	}

	days := 0
	if before, after, found := strings.Cut(value, "-"); found {
		parsed, err := strconv.Atoi(before)
		if err != nil {
			return 0, fmt.Errorf("%q has no day count before the dash", value)
		}
		days, value = parsed, after
	}

	fields := strings.Split(value, ":")
	if len(fields) < 2 || len(fields) > 3 {
		return 0, fmt.Errorf("%q is not [[dd-]hh:]mm:ss", value)
	}

	units := []time.Duration{time.Minute, time.Second}
	if len(fields) == 3 {
		units = []time.Duration{time.Hour, time.Minute, time.Second}
	}

	total := time.Duration(days) * 24 * time.Hour
	for i, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%q is not [[dd-]hh:]mm:ss", value)
		}
		total += time.Duration(n) * units[i]
	}
	return total, nil
}

// SystemClipboard writes text using whichever clipboard tool the platform has.
func SystemClipboard(text string) error {
	candidates := [][]string{{"pbcopy"}}
	if runtime.GOOS != "darwin" {
		candidates = [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "--clipboard", "--input"}}
	}

	// A tool being installed does not mean it works: wl-copy ships as a package
	// dependency on X11 boxes and fails at runtime with no Wayland session, so a
	// failing candidate must fall through to the next rather than abort.
	var lastErr error
	for _, argv := range candidates {
		path, err := exec.LookPath(argv[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(path, argv[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			lastErr = fmt.Errorf("running %s: %w", argv[0], err)
			continue
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("no clipboard tool found")
}

// SystemOpen opens url in the platform browser. The scheme is checked because
// the platform opener is not a browser: given a path it opens an application,
// and given a custom scheme it hands the value to whichever app registered for
// it. The URL arrives from gh's output, which gh resolved by running git in a
// directory a transcript named.
func SystemOpen(url string) error {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return fmt.Errorf("refusing to open %q: only http and https URLs are opened", url)
	}

	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	path, err := exec.LookPath(opener)
	if err != nil {
		return fmt.Errorf("finding %s: %w", opener, err)
	}
	if err := exec.Command(path, url).Run(); err != nil {
		return fmt.Errorf("running %s: %w", opener, err)
	}
	return nil
}
