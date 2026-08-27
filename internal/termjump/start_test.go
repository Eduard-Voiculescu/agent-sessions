package termjump_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eduardvoiculescu/agent-sessions/internal/termjump"
)

// startScript captures what Start would have handed osascript, so every
// assertion below reads the real script without an iTerm2 anywhere near it.
func startScript(t *testing.T, dir string, argv []string, reply string) (string, error) {
	t.Helper()

	var got string
	j := termjump.New(termjump.WithRunFunc(func(_ context.Context, script string) (string, error) {
		got = script
		return reply, nil
	}))

	err := j.Start(context.Background(), dir, argv)
	return got, err
}

func TestStartEntersTheDirectoryAndRunsTheAgentInANewTab(t *testing.T) {
	dir := t.TempDir()

	script, err := startScript(t, dir, []string{"claude"}, "FOUND")
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	for _, want := range []string{"create tab with default profile", "write text", "cd '" + dir + "' && 'claude'"} {
		if !strings.Contains(script, want) {
			t.Errorf("script is missing %q:\n%s", want, script)
		}
	}
}

// A directory comes out of an agent's own state file, which any process running
// as the owner can write, and write text hands its argument to a shell: an
// unquoted path is a command of somebody else's choosing.
func TestStartQuotesAPathThatWouldOtherwiseRunACommand(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "repo; touch pwned")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}

	script, err := startScript(t, dir, []string{"claude"}, "FOUND")
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	if !strings.Contains(script, "cd '"+dir+"'") {
		t.Errorf("the path is not quoted as one word:\n%s", script)
	}
}

func TestStartQuotesEveryArgvElement(t *testing.T) {
	script, err := startScript(t, t.TempDir(), []string{"claude", "--model", "opus 5"}, "FOUND")
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	if !strings.Contains(script, "&& 'claude' '--model' 'opus 5'") {
		t.Errorf("argv is not quoted element by element:\n%s", script)
	}
}

func TestStartRefusesAControlCharacterInTheDirectory(t *testing.T) {
	ran := false
	j := termjump.New(termjump.WithRunFunc(func(context.Context, string) (string, error) {
		ran = true
		return "FOUND", nil
	}))

	err := j.Start(context.Background(), t.TempDir()+"\nclaude --dangerously-skip-permissions", []string{"claude"})
	if err == nil {
		t.Fatal("Start() error = nil, want a refusal")
	}
	if ran {
		t.Error("the script ran despite the refusal")
	}
}

func TestStartRefusesAControlCharacterInArgv(t *testing.T) {
	_, err := startScript(t, t.TempDir(), []string{"claude\rrm -rf ~"}, "FOUND")
	if err == nil {
		t.Fatal("Start() error = nil, want a refusal")
	}
}

func TestStartRefusesAnEmptyArgv(t *testing.T) {
	if _, err := startScript(t, t.TempDir(), nil, "FOUND"); err == nil {
		t.Fatal("Start() error = nil, want a refusal")
	}
}

func TestStartRefusesADirectoryThatIsNotThere(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")

	_, err := startScript(t, missing, []string{"claude"}, "FOUND")
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("Start() error = %v, want it to name %s", err, missing)
	}
}

func TestStartRefusesAFileWhereADirectoryBelongs(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("creating the file: %v", err)
	}

	_, err := startScript(t, file, []string{"claude"}, "FOUND")
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("Start() error = %v, want it to say the path is not a directory", err)
	}
}

func TestStartTranslatesTheScriptsMarkers(t *testing.T) {
	tests := []struct {
		reply string
		want  string
	}{
		{"NOT_RUNNING", "iTerm2 is not running"},
		{"NO_WINDOWS", "no windows open"},
		{"WHAT", "unexpected osascript output"},
	}

	for _, tt := range tests {
		t.Run(tt.reply, func(t *testing.T) {
			_, err := startScript(t, t.TempDir(), []string{"claude"}, tt.reply)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Start() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

// Start shells out to osascript, which blocks on the Automation consent dialog
// the first time, and it is called from bubbletea's event loop.
func TestStartBoundsTheSubprocessWithADeadline(t *testing.T) {
	var deadline bool
	j := termjump.New(termjump.WithRunFunc(func(ctx context.Context, _ string) (string, error) {
		_, deadline = ctx.Deadline()
		return "FOUND", nil
	}))

	if err := j.Start(context.Background(), t.TempDir(), []string{"claude"}); err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	if !deadline {
		t.Error("the AppleScript ran with no deadline")
	}
}
