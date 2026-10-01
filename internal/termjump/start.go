package termjump

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"unicode"
)

// Start opens a new iTerm2 tab in the current window, enters dir, and runs argv
// there. Focus is not taken: the caller stays in the picker and the new session
// appears in it on the next tick, which is the same bargain Send makes.
//
// The tab starts the owner's own shell rather than exec'ing argv directly,
// because an agent installed through nvm, mise or asdf is on the PATH that a
// shell's rc files build and on no other. argv is not exec'd over the shell
// either, so the tab drops back to a prompt when the agent exits instead of
// closing.
func (j *Jumper) Start(ctx context.Context, dir string, argv []string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("start needs macOS and iTerm2")
	}

	command, err := startCommand(dir, argv)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, paneTimeout)
	defer cancel()

	return j.eachInstance(ctx, "start",
		func(app string) string { return startScript(app, command) },
		errors.New("iTerm2 opened no tab to start in"))
}

// startCommand builds the one shell line the new tab is written. Both the
// directory and every argv element are quoted as single words: a directory is
// read out of an agent's own state file, which any process running as the owner
// can write, and iTerm2's write text hands what it is given to a shell — so an
// unquoted path is a command of somebody else's choosing rather than a place to
// work in.
func startCommand(dir string, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("no command to start")
	}
	if err := controlFree("the directory", dir); err != nil {
		return "", err
	}
	for _, arg := range argv {
		if err := controlFree("the command", arg); err != nil {
			return "", err
		}
	}

	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("cannot start in %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cannot start in %s: not a directory", dir)
	}

	quoted := make([]string, 0, len(argv))
	for _, arg := range argv {
		quoted = append(quoted, shellQuote(arg))
	}
	return "cd " + shellQuote(dir) + " && " + strings.Join(quoted, " "), nil
}

// controlFree refuses a control character rather than escaping it, the way Send
// does: appleScriptString escapes the backslash and the double quote and nothing
// else, and a newline reaching the shell would end this command line and have
// its remainder read as the next one.
func controlFree(what, value string) error {
	if i := strings.IndexFunc(value, unicode.IsControl); i >= 0 {
		return fmt.Errorf("%s has a control character at byte %d", what, i)
	}
	return nil
}

// shellQuote wraps a value as one single-quoted shell word. action.ShellQuote is
// the same two lines and stays where it is: this package drives iTerm2 for the
// picker without depending on the action layer above it, and a security-critical
// quoter is not worth a new dependency edge.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// startScript creates the tab and writes the command into it. Only two of the
// four markers can come back: there is no pane to look for, so NOT_FOUND has
// nothing to report here.
func startScript(app, command string) string {
	return fmt.Sprintf(`
if application %s is running then
	tell application %[1]s
		if (count of windows) is 0 then return %q
		tell current window
			set opened to (create tab with default profile)
			tell current session of opened to write text %s
		end tell
		return %q
	end tell
else
	return %q
end if
`, appleScriptString(app), noWindowsMarker, appleScriptString(command), foundMarker, notRunningMarker)
}
