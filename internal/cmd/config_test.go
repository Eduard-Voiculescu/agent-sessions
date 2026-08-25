package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eduardvoiculescu/agent-sessions/internal/action"
	"github.com/eduardvoiculescu/agent-sessions/internal/config"
)

// renderedConfig drives the command the way the runtime does: flags parsed,
// config applied beneath them, then rendered.
func renderedConfig(t *testing.T, args []string, file config.File) string {
	t.Helper()

	opts := &Options{}
	root := newRootCommand(opts, noopRunners())
	if err := root.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(root, opts, file); err != nil {
		t.Fatalf("applyConfig() error = %v", err)
	}

	out := &bytes.Buffer{}
	cmd := newConfigCommand(opts)
	cmd.SetOut(out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return out.String()
}

// The question an owner has when an action is missing is "where did this value
// come from", so every line answers it.
func TestConfigReportsWhereEachValueCameFrom(t *testing.T) {
	out := renderedConfig(t, []string{"--linear-workspace", "from-flag"}, config.File{
		Path:    "/tmp/x/config",
		Editor:  "zed",
		Tickets: config.Tickets{Prefixes: []string{"cfg"}},
	})

	for _, want := range []string{
		"/tmp/x/config",
		"Zed", "(config)",
		"from-flag",
		"cfg",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("config output does not contain %q:\n%s", want, out)
		}
	}

	// The flag beat the config, so the workspace's annotation must not claim the
	// file supplied it.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "workspace") && strings.Contains(line, "(config)") {
			t.Errorf("workspace is annotated as coming from the config, but the flag won: %q", line)
		}
	}
}

// A machine with no file must say so, rather than printing a path that looks
// like it was read.
func TestConfigSaysWhenThereIsNoFile(t *testing.T) {
	out := renderedConfig(t, nil, config.File{})

	if !strings.Contains(out, "none") {
		t.Errorf("config output does not report the absent file:\n%s", out)
	}
	if !strings.Contains(out, "(detected)") && !strings.Contains(out, "(none installed)") {
		t.Errorf("config output does not say the tools were detected rather than configured:\n%s", out)
	}
}

func TestConfigListsEveryActionAndWhetherItIsHidden(t *testing.T) {
	out := renderedConfig(t, nil, config.File{Hidden: []string{"copy.transcript"}})

	opts := &Options{}
	for _, id := range actionRegistry(opts, registryFor(opts)).IDs(agentNames()) {
		if !strings.Contains(out, id) {
			t.Errorf("config output omits action %q:\n%s", id, out)
		}
	}

	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "copy.transcript") {
			continue
		}
		if !strings.Contains(line, "hidden") {
			t.Errorf("the hidden action is not marked as hidden: %q", line)
		}
	}
	if !strings.Contains(out, "shown") {
		t.Errorf("config output never marks an action as shown:\n%s", out)
	}
}

// A starter file that did not parse would be a trap: the first thing anyone
// does with it is uncomment a line.
func TestConfigInitWritesAFileThatParsesBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config")

	if err := writeStarter(path, false); err != nil {
		t.Fatalf("writeStarter() error = %v", err)
	}
	if _, err := config.LoadFrom(path); err != nil {
		t.Errorf("the starter file does not parse: %v", err)
	}
}

// Uncommenting one line at a time is how the file is used, so every commented
// line must be valid once its # is removed.
func TestEveryLineOfTheStarterIsValidWhenUncommented(t *testing.T) {
	dir := t.TempDir()
	section := ""

	for _, line := range strings.Split(starter, "\n") {
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "["):
			section = trimmed
			continue
		case !strings.Contains(trimmed, "="):
			// Prose in the header block, not a setting.
			continue
		}

		path := filepath.Join(dir, "probe")
		body := section + "\n" + trimmed + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.LoadFrom(path); err != nil {
			t.Errorf("uncommenting %q under %q does not parse: %v", trimmed, section, err)
		}
	}
}

// The starter is written commented out throughout: creating it must not change
// how the picker behaves.
func TestTheStarterChangesNothingWhenWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := writeStarter(path, false); err != nil {
		t.Fatal(err)
	}

	file, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	if file.Editor != "" || file.VCS != "" || file.Forge != "" || len(file.Hidden) != 0 {
		t.Errorf("the starter file turns settings on: %+v", file)
	}
	if file.Tickets.Provider != "" || file.Tickets.Workspace != "" || len(file.Tickets.Prefixes) != 0 {
		t.Errorf("the starter file turns ticket settings on: %+v", file.Tickets)
	}
}

// Clobbering a file someone has edited is not recoverable, so it takes an
// explicit --force.
func TestConfigInitRefusesToClobberWithoutForce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("[editor]\ndefault = zed\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := writeStarter(path, false); err == nil {
		t.Fatal("writeStarter() overwrote an existing file without --force")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "zed") {
		t.Error("the existing file was modified despite the refusal")
	}

	if err := writeStarter(path, true); err != nil {
		t.Errorf("writeStarter(force) error = %v", err)
	}
	body, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "default = zed") {
		t.Error("--force did not overwrite the file")
	}
}

// The starter advertises tool names, and one that does not resolve would send
// the owner down a dead end.
func TestTheStarterOnlyAdvertisesToolsThatResolve(t *testing.T) {
	for _, name := range action.EditorNames() {
		if !strings.Contains(starter, name) {
			t.Errorf("the starter does not mention the accepted editor %q", name)
		}
	}
	for _, name := range action.VCSNames() {
		if !strings.Contains(starter, name) {
			t.Errorf("the starter does not mention the accepted vcs client %q", name)
		}
	}
}

// The annotation has to name where the value actually came from. An environment
// variable overrides the config file, and reporting that value as "(config)"
// points the owner at a file that does not contain it.
func TestSourceAnnotationFollowsThePrecedenceChain(t *testing.T) {
	file := config.File{Path: "/tmp/x/config", Tickets: config.Tickets{Workspace: "from-config"}}

	for _, tt := range []struct {
		name  string
		args  []string
		env   string
		want  string
		value string
	}{
		{name: "from the config file", want: "(config)", value: "from-config"},
		{name: "environment beats the file", env: "from-env", want: "(env)", value: "from-env"},
		{name: "flag beats both", args: []string{"--linear-workspace", "from-flag"}, env: "from-env", want: "(flag)", value: "from-flag"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AGENT_SESSIONS_LINEAR_WORKSPACE", tt.env)

			opts := &Options{}
			root := newRootCommand(opts, noopRunners())
			if err := root.ParseFlags(tt.args); err != nil {
				t.Fatal(err)
			}
			if err := applyConfig(root, opts, file); err != nil {
				t.Fatalf("applyConfig() error = %v", err)
			}
			if err := resolveEnvFallbacks(root, opts); err != nil {
				t.Fatalf("resolveEnvFallbacks() error = %v", err)
			}

			if opts.Workspace != tt.value {
				t.Errorf("Workspace = %q, want %q", opts.Workspace, tt.value)
			}
			if got := source(opts, "workspace", "unset"); got != tt.want {
				t.Errorf("annotation = %q, want %q", got, tt.want)
			}
		})
	}
}
