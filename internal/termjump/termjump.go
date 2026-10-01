// Package termjump reaches the iTerm2 pane running a given pid: it can move
// focus there, so a session found waiting on a permission prompt is reached
// directly instead of resuming a second view of it, and it can type a line
// into it without taking focus at all. Only iTerm2 is covered because it is
// the terminal this tool is driven from — Terminal.app publishes `tty of tab`
// and tmux `#{pane_tty}`, so the restriction is scope, not capability.
package termjump

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// paneScript's four return values, matched back out of the run func's output
// in pane.
const (
	foundMarker      = "FOUND"
	notFoundMarker   = "NOT_FOUND"
	notRunningMarker = "NOT_RUNNING"
	noWindowsMarker  = "NO_WINDOWS"
)

// paneTimeout bounds one pane operation end to end, because both side effects
// can block on a human: osascript waits out macOS's one-time Automation
// consent dialog, and an unresponsive iTerm2 never answers the Apple event at
// all.
const paneTimeout = 10 * time.Second

// errProcessGone is the failure shape ps reports for a pid that has already
// exited: exit status 1 with nothing on either stream. It is separated from a
// broken ps call so the caller can be told which happened.
var errProcessGone = errors.New("process is gone")

// iTermExecutable is the tail every iTerm2 build's main executable path ends
// in, whatever the bundle is named or wherever it was built.
const iTermExecutable = ".app/Contents/MacOS/iTerm2"

// Jumper switches iTerm2's focus to the pane running a given pid. Its side
// effects are injected so a test can drive Jump without spawning ps or
// osascript, and without moving the caller's own focus.
type Jumper struct {
	tty  func(ctx context.Context, pid int) (string, error)
	apps func(ctx context.Context) ([]string, error)
	run  func(ctx context.Context, script string) (string, error)
}

// Option configures a Jumper built by New.
type Option func(*Jumper)

// WithTTYFunc overrides how a pid's controlling terminal is resolved. The
// default runs `ps -o tty= -p <pid>`.
func WithTTYFunc(fn func(context.Context, int) (string, error)) Option {
	return func(j *Jumper) { j.tty = fn }
}

// WithAppsFunc overrides how the running iTerm2 bundles are listed. The
// default reads them out of `ps -axo comm=`.
func WithAppsFunc(fn func(context.Context) ([]string, error)) Option {
	return func(j *Jumper) { j.apps = fn }
}

// WithRunFunc overrides how the generated AppleScript is executed. The
// default runs it through osascript.
func WithRunFunc(fn func(context.Context, string) (string, error)) Option {
	return func(j *Jumper) { j.run = fn }
}

// New builds a Jumper against the real ps and osascript, unless overridden.
func New(opts ...Option) *Jumper {
	j := &Jumper{tty: defaultTTY, apps: defaultApps, run: defaultRun}
	for _, opt := range opts {
		opt(j)
	}
	return j
}

// Jump switches iTerm2's focus to the pane running pid, or reports why it
// could not: the platform lacks iTerm2, the pid has exited, ps could not place
// it on a terminal, iTerm2 is not running or has no windows, or no pane's tty
// matches the one it found.
func (j *Jumper) Jump(ctx context.Context, pid int) error {
	return j.pane(ctx, pid, "jump", jumpScript)
}

// Send types text into the pane running pid and submits it, as though it had
// been typed there. Nothing is selected and the app is not activated: the
// caller stays where it is and watches the reply arrive, which is the whole
// reason to send rather than jump.
//
// A control character is refused rather than escaped. iTerm2 submits the line
// itself, so an embedded newline could only submit a fragment early, and the
// remaining control codes would reach the pane as terminal escapes rather than
// as the message someone meant to send.
func (j *Jumper) Send(ctx context.Context, pid int, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("nothing to send")
	}
	if i := strings.IndexFunc(text, unicode.IsControl); i >= 0 {
		return fmt.Errorf("message has a control character at byte %d", i)
	}
	return j.pane(ctx, pid, "send", func(app, tty string) string { return sendScript(app, tty, text) })
}

// pane resolves pid's controlling terminal, runs the script built for it, and
// translates the four markers back into errors. ctx bounds the two
// subprocesses; paneTimeout caps them even when the caller's context has no
// deadline of its own. verb names the operation in the errors that are about
// the operation rather than about the pane.
func (j *Jumper) pane(ctx context.Context, pid int, verb string, build func(app, tty string) string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("%s needs macOS and iTerm2", verb)
	}

	ctx, cancel := context.WithTimeout(ctx, paneTimeout)
	defer cancel()

	raw, err := j.tty(ctx, pid)
	switch {
	case errors.Is(err, errProcessGone):
		return fmt.Errorf("pid %d is no longer running", pid)
	case err != nil:
		return fmt.Errorf("resolving tty for pid %d: %w", pid, err)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "??" {
		return fmt.Errorf("pid %d has no controlling terminal", pid)
	}
	// ps prints the bare device name (ttys002); iTerm2 reports a session's tty
	// property in full path form (/dev/ttys002). The two never compare equal
	// unprefixed.
	tty := "/dev/" + raw

	return j.eachInstance(ctx, verb,
		func(app string) string { return build(app, tty) },
		fmt.Errorf("no iTerm2 pane found for pid %d", pid))
}

// eachInstance runs the script built for every running iTerm2 until one of
// them reports FOUND. A development build shares the release's bundle id, so
// `application "iTerm2"` resolves to whichever one LaunchServices prefers —
// often a freshly launched build with no windows — and the pane being looked
// for is in the other. Each instance is therefore addressed by its bundle
// path, and when none succeeds the most telling miss is the one reported.
func (j *Jumper) eachInstance(ctx context.Context, verb string, build func(app string) string, notFound error) error {
	apps, err := j.apps(ctx)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%s timed out listing iTerm2 instances: %w", verb, err)
		}
		return fmt.Errorf("listing running iTerm2 instances: %w", err)
	}
	if len(apps) == 0 {
		apps = []string{"iTerm2"}
	}

	miss := notRunningMarker
	for _, app := range apps {
		out, err := j.run(ctx, build(app))
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("%s timed out waiting for iTerm2: %w", verb, err)
			}
			return fmt.Errorf("running osascript: %w", err)
		}
		marker := strings.TrimSpace(out)
		if missRank(marker) < 0 {
			return outcome(marker, notFound)
		}
		if missRank(marker) > missRank(miss) {
			miss = marker
		}
	}
	return outcome(miss, notFound)
}

// missRank orders the markers that let the search move on to the next
// instance by how much they say, and is -1 for every other reply.
func missRank(marker string) int {
	switch marker {
	case notRunningMarker:
		return 0
	case noWindowsMarker:
		return 1
	case notFoundMarker:
		return 2
	default:
		return -1
	}
}

// outcome translates a script's marker back into an error. What NOT_FOUND means
// is the caller's to say: only the operation that ran knows what it was looking
// for, and creating a tab looks for nothing at all.
func outcome(out string, notFound error) error {
	switch strings.TrimSpace(out) {
	case foundMarker:
		return nil
	case notRunningMarker:
		return errors.New("iTerm2 is not running")
	case noWindowsMarker:
		return errors.New("iTerm2 is running but has no windows open")
	case notFoundMarker:
		return notFound
	default:
		return fmt.Errorf("unexpected osascript output %q", out)
	}
}

// jumpScript selects the matching pane and brings iTerm2 forward. Split panes
// mean the matching session shares its tab and window with siblings, so all
// three levels are selected, window first and session last — selecting only
// the session would leave a stale tab or window in front of it.
func jumpScript(app, tty string) string {
	return paneScript(app, tty, `select w
					select t
					select s
					-- A tab remembers its own current session, so selecting a tab after
					-- the session resets the session selection back to the tab's remembered one.
					-- Selecting in window-then-tab-then-session order ensures the final
					-- selection sticks.
					activate`)
}

// sendScript types text into the matching pane. iTerm2's write command enters
// the text as though it had been typed and submits it, so no trailing return
// is added; nothing is selected, so focus stays where the caller left it.
func sendScript(app, tty, text string) string {
	return paneScript(app, tty, "tell s to write text "+appleScriptString(text))
}

// paneScript walks every window, tab and session looking for the one pane whose
// tty matches, and runs action on it. A window count of zero is reported apart
// from a walk that found nothing, since only the latter says the pid's terminal
// is not iTerm2.
func paneScript(app, tty, action string) string {
	return fmt.Sprintf(`
if application %[1]s is running then
	tell application %[1]s
		if (count of windows) is 0 then return %[2]q
		repeat with w in windows
			repeat with t in tabs of w
				repeat with s in sessions of t
					if (tty of s) is %[3]s then
						%[4]s
						return %[5]q
					end if
				end repeat
			end repeat
		end repeat
	end tell
	return %[6]q
else
	return %[7]q
end if
`, appleScriptString(app), noWindowsMarker, appleScriptString(tty), action, foundMarker, notFoundMarker, notRunningMarker)
}

// appleScriptString quotes a value as an AppleScript string literal. Only the
// backslash and the double quote are escaped, and they are escaped by hand
// rather than with %q, which also emits \a, \v and \x00 forms that AppleScript
// does not decode — a tab in a message would arrive as the four literal
// characters \x09 instead. Send refuses control characters for that reason, so
// what reaches here is text whose only dangerous characters are these two: no
// device name or message can close the string and have its remainder read as
// script.
func appleScriptString(value string) string {
	var quoted strings.Builder
	quoted.Grow(len(value) + 2)
	quoted.WriteByte('"')
	for _, r := range value {
		if r == '\\' || r == '"' {
			quoted.WriteByte('\\')
		}
		quoted.WriteRune(r)
	}
	quoted.WriteByte('"')
	return quoted.String()
}

func defaultTTY(ctx context.Context, pid int) (string, error) {
	out, err := exec.CommandContext(ctx, "ps", "-o", "tty=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", psError(err, out)
	}
	return string(out), nil
}

// psError tells a pid that has exited apart from a ps that could not run: both
// exit 1, and only the dead pid leaves stdout and stderr empty.
func psError(err error, out []byte) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) &&
		strings.TrimSpace(string(out)) == "" &&
		strings.TrimSpace(string(exitErr.Stderr)) == "" {
		return errProcessGone
	}
	return withStderr(err)
}

// defaultApps lists the bundle path of every running iTerm2. comm is the full
// executable path on macOS, so a bundle path is what is left once the
// executable's tail inside the bundle is cut off.
func defaultApps(ctx context.Context) ([]string, error) {
	out, err := exec.CommandContext(ctx, "ps", "-axo", "comm=").Output()
	if err != nil {
		return nil, withStderr(err)
	}
	var apps []string
	for line := range strings.Lines(string(out)) {
		if bundle, ok := strings.CutSuffix(strings.TrimSpace(line), iTermExecutable); ok {
			apps = append(apps, bundle+".app")
		}
	}
	return apps, nil
}

func defaultRun(ctx context.Context, script string) (string, error) {
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	if err != nil {
		return "", withStderr(err)
	}
	return string(out), nil
}

// withStderr folds the subprocess's diagnostic into the error, because
// ExitError.Error() prints only "exit status 1": a denied Automation consent
// dialog, an AppleScript syntax error (-2741) and a missing app (-1728) all
// arrive that way, and the first of the three is fixed in System Settings →
// Privacy & Security → Automation rather than in this program.
func withStderr(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if msg := strings.TrimSpace(string(exitErr.Stderr)); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
	}
	return err
}
