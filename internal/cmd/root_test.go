package cmd

import (
	"bytes"
	"cmp"
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/eduardvoiculescu/agent-sessions/internal/action"
	"github.com/eduardvoiculescu/agent-sessions/internal/config"
	"github.com/eduardvoiculescu/agent-sessions/internal/provider"
	"github.com/eduardvoiculescu/agent-sessions/internal/provider/claude"
	"github.com/eduardvoiculescu/agent-sessions/internal/provider/opencode"
	"github.com/eduardvoiculescu/agent-sessions/internal/session"
	"github.com/eduardvoiculescu/agent-sessions/internal/ui"
)

func TestRootCommandFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want Options
	}{
		{
			name: "defaults",
			args: []string{"list"},
			want: Options{Limit: 200},
		},
		{
			name: "all flags set",
			args: []string{"list", "--limit", "5", "--live", "--cwd", "/tmp", "--json", "--claude-dir", "/c"},
			want: Options{Limit: 5, LiveOnly: true, Cwd: "/tmp", JSON: true, ClaudeDir: "/c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Options
			root := newRootCommand(&got, noopRunners())
			root.SetOut(&bytes.Buffer{})
			root.SetArgs(tt.args)

			if err := root.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			// Compared field by field: Options also carries what the config file
			// resolved, including a func and a map, and neither compares
			// meaningfully with DeepEqual.
			flagged := Options{
				ClaudeDir:      got.ClaudeDir,
				Limit:          got.Limit,
				LiveOnly:       got.LiveOnly,
				Cwd:            got.Cwd,
				JSON:           got.JSON,
				Workspace:      got.Workspace,
				TicketPrefixes: got.TicketPrefixes,
			}
			if !reflect.DeepEqual(flagged, tt.want) {
				t.Errorf("Options = %+v, want %+v", flagged, tt.want)
			}
		})
	}
}

func TestRootCommandHelpMentionsList(t *testing.T) {
	var opts Options
	out := &bytes.Buffer{}
	root := newRootCommand(&opts, noopRunners())
	root.SetOut(out)
	root.SetArgs([]string{"--help"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out.String(), "list") {
		t.Errorf("help output missing the list subcommand:\n%s", out.String())
	}
}

// --json belongs to list; on the root it would be silently ignored by the picker.
func TestRootCommandRejectsTheListOnlyJSONFlag(t *testing.T) {
	var opts Options
	root := newRootCommand(&opts, noopRunners())
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--json"})

	if err := root.Execute(); err == nil {
		t.Error("Execute() error = nil, want --json rejected outside the list command")
	}
}

func noopRunners() runners {
	noop := func(*cobra.Command, *Options) error { return nil }
	return runners{pick: noop, list: noop}
}

func TestTicketFlags(t *testing.T) {
	var got Options
	root := newRootCommand(&got, noopRunners())
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"--linear-workspace", "acme", "--ticket-prefix", "abc", "--ticket-prefix", "eng"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got.Workspace != "acme" {
		t.Errorf("Workspace = %q, want %q", got.Workspace, "acme")
	}
	if len(got.TicketPrefixes) != 2 || got.TicketPrefixes[0] != "abc" || got.TicketPrefixes[1] != "eng" {
		t.Errorf("TicketPrefixes = %v, want [abc eng]", got.TicketPrefixes)
	}
}

// Only the picker opens tickets; on the persistent set these flags were accepted
// by every subcommand and then silently ignored, exactly the defect --json was
// already fixed for.
func TestTicketFlagsAreRejectedOutsideThePicker(t *testing.T) {
	tests := [][]string{
		{"purge", "--linear-workspace", "acme"},
		{"purge", "--ticket-prefix", "eng"},
		{"list", "--linear-workspace", "acme"},
		{"list", "--ticket-prefix", "eng"},
	}

	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var opts Options
			root := newRootCommand(&opts, noopRunners())
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(args)

			if err := root.Execute(); err == nil {
				t.Errorf("Execute(%v) error = nil, want the flag rejected where it does nothing", args)
			}
		})
	}
}

// The picker's header block reports LiveOnly, Cwd, TicketPrefixes and
// ClaudeDir from Config, which ui.Run has no other way to learn since Filter
// is an opaque closure. This is checked against pickerConfig rather than
// through a rendered picker because ui.Run blocks on a real terminal.
func TestPickerConfigReflectsOptions(t *testing.T) {
	opts := &Options{
		LiveOnly:       true,
		Cwd:            "/keep/repo",
		TicketPrefixes: []string{"abc", "eng"},
		ClaudeDir:      "/opt/claude-config",
	}
	registry := provider.NewRegistry(claude.New(t.TempDir()))

	cfg := pickerConfig(context.Background(), opts, nil, nil, registry)

	if !cfg.LiveOnly() {
		t.Error("Config.LiveOnly() = false, want true")
	}
	if got := cfg.Cwd(); got != "/keep/repo" {
		t.Errorf("Config.Cwd() = %q, want %q", got, "/keep/repo")
	}
	if !reflect.DeepEqual(cfg.TicketPrefixes, []string{"abc", "eng"}) {
		t.Errorf("Config.TicketPrefixes = %v, want [abc eng]", cfg.TicketPrefixes)
	}
	if cfg.ClaudeDir != "/opt/claude-config" {
		t.Errorf("Config.ClaudeDir = %q, want %q", cfg.ClaudeDir, "/opt/claude-config")
	}
}

// The picker's header block reads the filters through these accessors on every
// frame. A palette command mutates the same opts the Filter closure reads, so a
// value copied into Config here would leave the header naming the filters the
// picker started with while the rows obey the new ones.
func TestPickerConfigReadsFiltersLiveThroughItsAccessors(t *testing.T) {
	opts := &Options{Cwd: "/keep/repo"}
	registry := provider.NewRegistry(claude.New(t.TempDir()))

	cfg := pickerConfig(context.Background(), opts, nil, nil, registry)

	for _, c := range globalCommands(opts) {
		if !strings.Contains(c.Label, "--live") {
			continue
		}
		if _, err := c.Run(); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}
	if !cfg.LiveOnly() {
		t.Error("Config.LiveOnly() = false after the toggle command flipped it on")
	}

	for _, c := range globalCommands(opts) {
		if !strings.Contains(c.Label, "--cwd") {
			continue
		}
		if _, err := c.Run(); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}
	if got := cfg.Cwd(); got != "" {
		t.Errorf("Config.Cwd() = %q after the clear command ran, want it empty", got)
	}
}

// With no --claude-dir flag, the header's directory line must still name the
// directory sessions actually loaded from, the same fallback loadSessions and
// the empty-list error already use.
func TestPickerConfigFallsBackToTheDefaultClaudeDir(t *testing.T) {
	opts := &Options{}
	registry := provider.NewRegistry(claude.New(t.TempDir()))

	cfg := pickerConfig(context.Background(), opts, nil, nil, registry)

	if cfg.ClaudeDir != claude.Dir() {
		t.Errorf("Config.ClaudeDir = %q, want the default %q", cfg.ClaudeDir, claude.Dir())
	}
}

func TestActionsForUsesTheProvidersAliveCheck(t *testing.T) {
	asked := 0
	opts := &Options{}
	registry := provider.NewRegistry(claude.New(t.TempDir(), claude.WithAliveFunc(func(int) bool {
		asked++
		return false
	})))

	live := session.Session{Agent: "claude", ID: "s1", Transcript: "/tmp/s1.jsonl", Live: true, PID: 4242}
	ids := actionIDs(actionsFor(opts, registry)(live))

	if asked == 0 {
		t.Error("the provider's alive func was never consulted; kill would be gated by the action package's own default")
	}
	if contains(ids, "process.kill") {
		t.Errorf("actions = %v, want no kill entry once the provider reports the pid dead", ids)
	}
}

func TestResumeCommandCopiedFromThePaletteComesFromTheProvider(t *testing.T) {
	registry := provider.NewRegistry(claude.New(t.TempDir()))
	s := session.Session{Agent: "claude", ID: "abc", Cwd: "/Users/x/My Project"}

	got, err := resumeCommand(registry, s)
	if err != nil {
		t.Fatalf("resumeCommand() error = %v", err)
	}
	want := `cd '/Users/x/My Project' && 'claude' '--resume' 'abc'`
	if got != want {
		t.Errorf("resumeCommand() = %q, want %q", got, want)
	}

	var copied string
	reg := action.NewRegistry(action.Config{
		Clipboard:     func(text string) error { copied = text; return nil },
		ResumeCommand: func(s session.Session) (string, error) { return resumeCommand(registry, s) },
	})
	for _, a := range reg.For(s) {
		if a.ID() != "copy.resume" {
			continue
		}
		if _, err := a.Run(context.Background(), s); err != nil {
			t.Fatalf("copy.resume Run() error = %v", err)
		}
	}
	if copied != want {
		t.Errorf("clipboard = %q, want the provider's argv %q", copied, want)
	}
}

func TestResumeCommandRefusesAnUnregisteredAgent(t *testing.T) {
	registry := provider.NewRegistry(claude.New(t.TempDir()))

	if _, err := resumeCommand(registry, session.Session{Agent: "codex", ID: "abc"}); err == nil {
		t.Error("resumeCommand() error = nil, want a refusal rather than a claude command for another agent")
	}
}

// The picker's Preview closure has to look the session's own provider up by
// agent, the same as resumeCommand does, rather than parsing every
// transcript as claude's own format.
func TestPreviewForReadsTheSessionsTranscriptThroughItsProvider(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/s1.jsonl"
	body := `{"type":"user","sessionId":"s1","message":{"role":"user","content":"hello there"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	registry := provider.NewRegistry(claude.New(t.TempDir()))
	cfg := pickerConfig(context.Background(), &Options{}, nil, nil, registry)

	got, err := cfg.Preview(session.Session{Agent: "claude", ID: "s1", Transcript: path})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if len(got) != 1 || got[0].Text != "hello there" {
		t.Errorf("Preview() = %+v, want the one message the transcript holds", got)
	}
}

func TestPreviewForRefusesAnUnregisteredAgent(t *testing.T) {
	registry := provider.NewRegistry(claude.New(t.TempDir()))
	cfg := pickerConfig(context.Background(), &Options{}, nil, nil, registry)

	if _, err := cfg.Preview(session.Session{Agent: "codex", ID: "abc"}); err == nil {
		t.Error("Preview() error = nil, want a refusal rather than reading another agent's transcript as claude's format")
	}
}

func TestGlobalCommandsReflectActiveFilters(t *testing.T) {
	opts := &Options{}
	if labels := commandLabels(globalCommands(opts)); contains(labels, "clear --cwd filter") {
		t.Errorf("commands = %v, want no clear-cwd entry when no cwd filter is set", labels)
	}

	opts.Cwd = "/Users/dev/git"
	if labels := commandLabels(globalCommands(opts)); !contains(labels, "clear --cwd filter") {
		t.Errorf("commands = %v, want a clear-cwd entry when a cwd filter is set", labels)
	}
}

func TestToggleLiveCommandFlipsTheOption(t *testing.T) {
	opts := &Options{}
	cmds := globalCommands(opts)

	var toggle Command
	for _, c := range cmds {
		if strings.Contains(c.Label, "--live") {
			toggle = c
		}
	}
	if toggle.Run == nil {
		t.Fatal("no --live toggle among the global commands")
	}

	if _, err := toggle.Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !opts.LiveOnly {
		t.Error("LiveOnly = false after toggling it on")
	}
	if _, err := toggle.Run(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if opts.LiveOnly {
		t.Error("LiveOnly = true after toggling it off again")
	}
}

// TestActionsForContributesProviderActions covers the action.Contributor type
// assertion in actionsFor, the one place internal/action learns about a
// concrete provider without importing internal/provider.
func TestActionsForContributesProviderActions(t *testing.T) {
	opts := &Options{}
	registry := provider.NewRegistry(claude.New(t.TempDir()))
	get := actionsFor(opts, registry)

	claudeSession := session.Session{Agent: "claude", ID: "s1", Transcript: "/tmp/s1.jsonl"}
	ids := actionIDs(get(claudeSession))
	if !contains(ids, "session.delete") {
		t.Errorf("actions for a claude session = %v, want the provider's contributed session.delete action", ids)
	}
	if !contains(ids, "copy.id") {
		t.Errorf("actions for a claude session = %v, want a built-in action alongside it", ids)
	}

	unregistered := session.Session{Agent: "no-such-agent", ID: "s2", Transcript: "/tmp/s2.jsonl"}
	unregisteredIDs := actionIDs(get(unregistered))
	if len(unregisteredIDs) == 0 {
		t.Error("actions for an unregistered agent = none, want the built-ins regardless")
	}
	if contains(unregisteredIDs, "session.delete") {
		t.Errorf("actions for an unregistered agent = %v, want no provider-contributed action", unregisteredIDs)
	}
}

func TestEnvironmentVariableFallbacks(t *testing.T) {
	tests := []struct {
		name                string
		workspace           string
		ticketPrefix        string
		linearWorkspaceFlag *string
		ticketPrefixFlags   []string
		wantWorkspace       string
		wantTicketPrefixes  []string
	}{
		{
			name:                "neither flag nor env set",
			workspace:           "",
			ticketPrefix:        "",
			linearWorkspaceFlag: nil,
			ticketPrefixFlags:   nil,
			wantWorkspace:       "",
			wantTicketPrefixes:  nil,
		},
		{
			name:                "env set, no flag",
			workspace:           "acme",
			ticketPrefix:        "abc,eng",
			linearWorkspaceFlag: nil,
			ticketPrefixFlags:   nil,
			wantWorkspace:       "acme",
			wantTicketPrefixes:  []string{"abc", "eng"},
		},
		{
			name:                "flag set, env also set - flag wins for workspace",
			workspace:           "acme",
			ticketPrefix:        "abc,eng",
			linearWorkspaceFlag: strPtr("override"),
			ticketPrefixFlags:   nil,
			wantWorkspace:       "override",
			wantTicketPrefixes:  []string{"abc", "eng"},
		},
		{
			name:                "flag set, env also set - flag wins for ticket-prefix",
			workspace:           "acme",
			ticketPrefix:        "abc,eng",
			linearWorkspaceFlag: nil,
			ticketPrefixFlags:   []string{"override"},
			wantWorkspace:       "acme",
			wantTicketPrefixes:  []string{"override"},
		},
		{
			name:                "flag explicitly empty with env set - flag wins",
			workspace:           "acme",
			ticketPrefix:        "abc,eng",
			linearWorkspaceFlag: strPtr(""),
			ticketPrefixFlags:   nil,
			wantWorkspace:       "",
			wantTicketPrefixes:  []string{"abc", "eng"},
		},
		{
			name:                "ticket-prefix with whitespace and trailing comma",
			workspace:           "",
			ticketPrefix:        "abc, eng,",
			linearWorkspaceFlag: nil,
			ticketPrefixFlags:   nil,
			wantWorkspace:       "",
			wantTicketPrefixes:  []string{"abc", "eng"},
		},
		{
			name:                "ticket-prefix with only whitespace entries",
			workspace:           "",
			ticketPrefix:        "abc, , eng",
			linearWorkspaceFlag: nil,
			ticketPrefixFlags:   nil,
			wantWorkspace:       "",
			wantTicketPrefixes:  []string{"abc", "eng"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.workspace != "" {
				t.Setenv("AGENT_SESSIONS_LINEAR_WORKSPACE", tt.workspace)
			}
			if tt.ticketPrefix != "" {
				t.Setenv("AGENT_SESSIONS_TICKET_PREFIX", tt.ticketPrefix)
			}

			var got Options
			root := newRootCommand(&got, noopRunners())
			root.SetOut(&bytes.Buffer{})

			args := []string{}
			if tt.linearWorkspaceFlag != nil {
				args = append(args, "--linear-workspace", *tt.linearWorkspaceFlag)
			}
			for _, prefix := range tt.ticketPrefixFlags {
				args = append(args, "--ticket-prefix", prefix)
			}

			root.SetArgs(args)

			if err := root.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if got.Workspace != tt.wantWorkspace {
				t.Errorf("Workspace = %q, want %q", got.Workspace, tt.wantWorkspace)
			}
			if !reflect.DeepEqual(got.TicketPrefixes, tt.wantTicketPrefixes) {
				t.Errorf("TicketPrefixes = %v, want %v", got.TicketPrefixes, tt.wantTicketPrefixes)
			}
		})
	}
}

// --ticket-prefix "" reached the action registry as one empty prefix, which its
// len(prefixes) checks read as configured: the ticket entry then vanished with no
// diagnostic, the one outcome its own comment says must not happen.
func TestTicketPrefixFlagIsNormalisedLikeTheEnvironment(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "an empty prefix is dropped", args: []string{"--ticket-prefix", ""}, want: nil},
		{name: "a whitespace-only prefix is dropped", args: []string{"--ticket-prefix", "   "}, want: nil},
		{name: "surrounding whitespace is trimmed", args: []string{"--ticket-prefix", "  eng  "}, want: []string{"eng"}},
		{name: "an empty prefix among real ones is dropped", args: []string{"--ticket-prefix", "abc", "--ticket-prefix", "", "--ticket-prefix", "eng"}, want: []string{"abc", "eng"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Options
			root := newRootCommand(&got, noopRunners())
			root.SetOut(&bytes.Buffer{})
			root.SetArgs(tt.args)

			if err := root.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !reflect.DeepEqual(got.TicketPrefixes, tt.want) {
				t.Errorf("TicketPrefixes = %#v, want %#v", got.TicketPrefixes, tt.want)
			}
		})
	}
}

func TestParseTicketPrefixes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "single value",
			input: "eng",
			want:  []string{"eng"},
		},
		{
			name:  "multiple values",
			input: "abc,eng",
			want:  []string{"abc", "eng"},
		},
		{
			name:  "values with spaces",
			input: "abc, eng",
			want:  []string{"abc", "eng"},
		},
		{
			name:  "trailing comma",
			input: "abc, eng,",
			want:  []string{"abc", "eng"},
		},
		{
			name:  "extra whitespace",
			input: "  abc  ,  eng  ",
			want:  []string{"abc", "eng"},
		},
		{
			name:  "empty entries filtered out",
			input: "abc,,eng",
			want:  []string{"abc", "eng"},
		},
		{
			name:  "whitespace-only entries filtered out",
			input: "abc, , eng",
			want:  []string{"abc", "eng"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTicketPrefixes(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseTicketPrefixes(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func strPtr(s string) *string {
	return &s
}

func actionIDs(acts []action.Action) []string {
	out := make([]string, 0, len(acts))
	for _, a := range acts {
		out = append(out, a.ID())
	}
	return out
}

func commandLabels(cmds []Command) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, c.Label)
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

// stubAgent is a provider with no Prompter: the seam has to be asked for, since
// not every coding agent takes an opening prompt on the command line.
type stubAgent struct{}

func (stubAgent) Name() string { return "stub" }

func (stubAgent) Discover(context.Context, int) ([]session.Session, error) { return nil, nil }

func (stubAgent) ResumeArgv(s session.Session, fork bool) ([]string, error) {
	argv := []string{"stub", "--resume", s.ID}
	if fork {
		argv = append(argv, "--fork")
	}
	return argv, nil
}

type promptingAgent struct{ stubAgent }

func (promptingAgent) ResumeWithPromptArgv(s session.Session, prompt string) ([]string, error) {
	return []string{"stub", "--resume", s.ID, "--", prompt}, nil
}

func TestResumeArgvRoutesAMessageThroughThePrompterSeam(t *testing.T) {
	choice := ui.Choice{Session: session.Session{ID: "s1"}, Message: "carry on"}

	got, err := resumeArgv(promptingAgent{}, choice)
	if err != nil {
		t.Fatalf("resumeArgv() error = %v", err)
	}
	want := []string{"stub", "--resume", "s1", "--", "carry on"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("resumeArgv() = %v, want %v", got, want)
	}
}

// A message aimed at an agent that cannot take one must fail loudly. Dropping
// it would resume the session and silently swallow what someone typed.
func TestResumeArgvRefusesAMessageAnAgentCannotCarry(t *testing.T) {
	choice := ui.Choice{Session: session.Session{ID: "s1"}, Message: "carry on"}

	got, err := resumeArgv(stubAgent{}, choice)
	if err == nil {
		t.Fatalf("resumeArgv() = %v, want an error for an agent with no prompt support", got)
	}
	if !strings.Contains(err.Error(), "stub") {
		t.Errorf("resumeArgv() error = %v, want it to name the agent", err)
	}
}

// The ordinary resume path must not start routing through the new seam: fork is
// only expressible there, and a plain Enter still has to be able to fork.
func TestResumeArgvWithNoMessageKeepsTheOrdinaryPath(t *testing.T) {
	choice := ui.Choice{Session: session.Session{ID: "s1"}, Fork: true}

	got, err := resumeArgv(promptingAgent{}, choice)
	if err != nil {
		t.Fatalf("resumeArgv() error = %v", err)
	}
	want := []string{"stub", "--resume", "s1", "--fork"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("resumeArgv() = %v, want %v", got, want)
	}
}

// A flag left at its default must not beat the config file, and a flag typed on
// purpose must. This is the assertion a naive implementation fails: checking a
// non-zero value rather than Flags().Changed conflates the two.
func TestConfigLosesToAnExplicitFlagAndBeatsAnUnsetOne(t *testing.T) {
	file := config.File{
		Path:    "/tmp/x/config",
		Editor:  "zed",
		Tickets: config.Tickets{Workspace: "from-config", Prefixes: []string{"cfg"}},
	}

	unset := &Options{}
	cmd := newRootCommand(unset, noopRunners())
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(cmd, unset, file); err != nil {
		t.Fatalf("applyConfig() error = %v", err)
	}
	if unset.Workspace != "from-config" {
		t.Errorf("Workspace = %q, want the config's value when the flag is unset", unset.Workspace)
	}
	if strings.Join(unset.TicketPrefixes, ",") != "cfg" {
		t.Errorf("TicketPrefixes = %v, want the config's list", unset.TicketPrefixes)
	}
	if unset.Editor.Name != "Zed" {
		t.Errorf("Editor = %+v, want the config's editor", unset.Editor)
	}
	if unset.ConfigPath != "/tmp/x/config" {
		t.Errorf("ConfigPath = %q, want the file it was read from", unset.ConfigPath)
	}

	typed := &Options{}
	cmd = newRootCommand(typed, noopRunners())
	if err := cmd.ParseFlags([]string{"--linear-workspace", "from-flag", "--ticket-prefix", "flg"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(cmd, typed, file); err != nil {
		t.Fatalf("applyConfig() error = %v", err)
	}
	if typed.Workspace != "from-flag" {
		t.Errorf("Workspace = %q, want the flag to win", typed.Workspace)
	}
	if strings.Join(typed.TicketPrefixes, ",") != "flg" {
		t.Errorf("TicketPrefixes = %v, want the flag to win", typed.TicketPrefixes)
	}
}

// The case that separates Changed from a zero-value check: a flag set
// explicitly to empty. By value it is indistinguishable from unset, so a
// zero-value check silently hands the setting back to the config file — and
// --linear-workspace "" is how someone turns the ticket action off for one run.
func TestAFlagSetToEmptyStillBeatsTheConfig(t *testing.T) {
	file := config.File{Tickets: config.Tickets{Workspace: "from-config", Prefixes: []string{"cfg"}}}

	opts := &Options{}
	cmd := newRootCommand(opts, noopRunners())
	if err := cmd.ParseFlags([]string{"--linear-workspace", "", "--ticket-prefix", ""}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(cmd, opts, file); err != nil {
		t.Fatalf("applyConfig() error = %v", err)
	}

	if opts.Workspace != "" {
		t.Errorf("Workspace = %q, want the explicitly empty flag to win over the config", opts.Workspace)
	}
	// Not len == 0: an explicitly empty flag arrives as one empty element and is
	// stripped later, by cleanTicketPrefixes. What matters here is that the
	// config's list did not take its place.
	if strings.Join(opts.TicketPrefixes, ",") == "cfg" {
		t.Errorf("TicketPrefixes = %v, want the explicitly empty flag to win over the config", opts.TicketPrefixes)
	}
}

// A name that is not in a table is fatal at startup. The alternative is a picker
// running with actions the owner did not ask for.
func TestUnknownNamesInTheConfigAreFatal(t *testing.T) {
	for _, tt := range []struct {
		name string
		file config.File
		want string
	}{
		{"editor", config.File{Editor: "helix"}, "unknown editor"},
		{"vcs", config.File{VCS: "magit"}, "unknown vcs"},
		{"ticket provider", config.File{Tickets: config.Tickets{Provider: "trac"}}, "unknown ticket provider"},
		{"forge provider", config.File{Forge: "gitlab"}, "unknown forge provider"},
		{"action id", config.File{Hidden: []string{"copy.cwdd"}}, "unknown action id"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			opts := &Options{}
			cmd := newRootCommand(opts, noopRunners())
			if err := cmd.ParseFlags(nil); err != nil {
				t.Fatal(err)
			}

			err := applyConfig(cmd, opts, tt.file)
			if err == nil {
				t.Fatalf("applyConfig() error = nil, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
			// The message must list what is valid, or the owner has nothing to
			// correct towards.
			if !strings.Contains(err.Error(), "known") {
				t.Errorf("error = %q, want it to name the valid values", err)
			}
		})
	}
}

// The path is carried into the error so the owner knows which of the three
// possible files to edit.
func TestUnknownNameNamesTheFileItCameFrom(t *testing.T) {
	opts := &Options{}
	cmd := newRootCommand(opts, noopRunners())
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatal(err)
	}

	err := applyConfig(cmd, opts, config.File{Path: "/home/e/.agent-sessions/config", Editor: "helix"})
	if err == nil || !strings.Contains(err.Error(), "/home/e/.agent-sessions/config") {
		t.Errorf("error = %v, want it to name the config file", err)
	}
}

// Hiding is validated against the registry's own ids, so a valid one is accepted
// and the check is not merely rejecting everything.
func TestConfigAcceptsEveryRealActionID(t *testing.T) {
	opts := &Options{}
	cmd := newRootCommand(opts, noopRunners())
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatal(err)
	}

	ids := actionRegistry(opts, registryFor(opts)).IDs(agentNames())
	if len(ids) == 0 {
		t.Fatal("the registry reports no action ids")
	}
	if err := applyConfig(cmd, opts, config.File{Hidden: ids}); err != nil {
		t.Errorf("applyConfig() error = %v, want every real id accepted", err)
	}
	if len(opts.Hidden) != len(ids) {
		t.Errorf("Hidden = %v, want all %d ids", opts.Hidden, len(ids))
	}
}

// Every second-wave key follows the same rule as the first: a flag typed on
// purpose wins, a flag left at its default does not.
func TestSecondWaveConfigLosesOnlyToAnExplicitFlag(t *testing.T) {
	limit := 50
	live := true
	file := config.File{
		Limit:     &limit,
		Live:      &live,
		Cwd:       "/srv/repos",
		ClaudeDir: "/opt/claude",
	}

	unset := &Options{}
	cmd := newRootCommand(unset, noopRunners())
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(cmd, unset, file); err != nil {
		t.Fatalf("applyConfig() error = %v", err)
	}
	if unset.Limit != 50 || !unset.LiveOnly || unset.Cwd != "/srv/repos" || unset.ClaudeDir != "/opt/claude" {
		t.Errorf("unset flags = %+v, want every value from the config", unset)
	}

	typed := &Options{}
	cmd = newRootCommand(typed, noopRunners())
	if err := cmd.ParseFlags([]string{"--limit", "7", "--live=false", "--cwd", "/flag/path", "--claude-dir", "/flag/claude"}); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(cmd, typed, file); err != nil {
		t.Fatalf("applyConfig() error = %v", err)
	}
	if typed.Limit != 7 {
		t.Errorf("Limit = %d, want the flag to win", typed.Limit)
	}
	// --live=false typed on purpose must beat a config that turned it on. By
	// value it is indistinguishable from unset, which is why this is Changed.
	if typed.LiveOnly {
		t.Error("LiveOnly = true, want the explicitly false flag to win over the config")
	}
	if typed.Cwd != "/flag/path" || typed.ClaudeDir != "/flag/claude" {
		t.Errorf("Cwd = %q, ClaudeDir = %q; want the flags to win", typed.Cwd, typed.ClaudeDir)
	}
}

// limit = 0 means no limit, and it has to survive the merge rather than being
// read as "nothing was set" and replaced by the flag's default of 200.
func TestConfigLimitOfZeroIsHonoured(t *testing.T) {
	zero := 0
	opts := &Options{}
	cmd := newRootCommand(opts, noopRunners())
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(cmd, opts, config.File{Limit: &zero}); err != nil {
		t.Fatalf("applyConfig() error = %v", err)
	}
	if opts.Limit != 0 {
		t.Errorf("Limit = %d, want 0 (no limit) as the config asked", opts.Limit)
	}
}

// $CLAUDE_CONFIG_DIR belongs to Claude Code and is this tool's fallback
// default, not somebody overriding this tool, so the config file outranks it.
// Only --claude-dir outranks the file.
func TestConfigClaudeDirBeatsTheClaudeEnvironmentVariable(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/from/env")

	opts := &Options{}
	cmd := newRootCommand(opts, noopRunners())
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(cmd, opts, config.File{ClaudeDir: "/from/config"}); err != nil {
		t.Fatalf("applyConfig() error = %v", err)
	}
	if err := resolveEnvFallbacks(cmd, opts); err != nil {
		t.Fatalf("resolveEnvFallbacks() error = %v", err)
	}

	if got := cmp.Or(opts.ClaudeDir, claude.Dir()); got != "/from/config" {
		t.Errorf("resolved claude dir = %q, want the config file to win over the environment", got)
	}
}

// The persistent flags are reached through the inherited set on a subcommand,
// so Changed has to be asked of Flags() and not of the root's own set.
func TestSecondWaveFlagsAreSeenOnASubcommand(t *testing.T) {
	limit := 50
	opts := &Options{}
	root := newRootCommand(opts, noopRunners())
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"list", "--limit", "9"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	list, _, err := root.Find([]string{"list"})
	if err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(list, opts, config.File{Limit: &limit}); err != nil {
		t.Fatalf("applyConfig() error = %v", err)
	}
	if opts.Limit != 9 {
		t.Errorf("Limit = %d, want the subcommand's flag to beat the config", opts.Limit)
	}
}

// A provider named in the config that the binary cannot build is fatal at
// startup, naming the ones it can.
func TestUnknownProviderInTheConfigIsFatal(t *testing.T) {
	opts := &Options{}
	cmd := newRootCommand(opts, noopRunners())
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatal(err)
	}

	err := applyConfig(cmd, opts, config.File{Path: "/tmp/c", Providers: []string{"claude", "goose"}})
	if err == nil {
		t.Fatal("applyConfig() error = nil, want the unknown provider reported")
	}
	for _, want := range []string{"unknown provider", "goose", "claude", "opencode"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestConfigCarriesTheProviderSettings(t *testing.T) {
	opts := &Options{}
	cmd := newRootCommand(opts, noopRunners())
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatal(err)
	}

	file := config.File{Providers: []string{"opencode"}, OpencodeDir: "/srv/opencode"}
	if err := applyConfig(cmd, opts, file); err != nil {
		t.Fatalf("applyConfig() error = %v", err)
	}
	if strings.Join(opts.Providers, ",") != "opencode" {
		t.Errorf("Providers = %v, want [opencode]", opts.Providers)
	}
	if opts.OpencodeDir != "/srv/opencode" {
		t.Errorf("OpencodeDir = %q, want the configured path", opts.OpencodeDir)
	}
}

// readOnlyProvider implements no Starter, which is how a provider that can only
// read stored sessions stays legal.
type readOnlyProvider struct{}

func (readOnlyProvider) Name() string { return "readonly" }

func (readOnlyProvider) Discover(context.Context, int) ([]session.Session, error) { return nil, nil }

func (readOnlyProvider) ResumeArgv(session.Session, bool) ([]string, error) { return nil, nil }

func TestPickerConfigPublishesTheStartableAgents(t *testing.T) {
	registry := provider.NewRegistry(claude.New(t.TempDir()), opencode.New(t.TempDir()), readOnlyProvider{})

	cfg := pickerConfig(context.Background(), &Options{}, nil, nil, registry)

	if !reflect.DeepEqual(cfg.StartAgents, []string{"claude", "opencode"}) {
		t.Errorf("Config.StartAgents = %v, want [claude opencode]", cfg.StartAgents)
	}
	if cfg.Start == nil {
		t.Error("Config.Start is nil, want the spawn wired")
	}
}

func TestStartSessionHandsTheProvidersOwnArgvToTheTerminal(t *testing.T) {
	var gotDir string
	var gotArgv []string
	start := startSession(provider.NewRegistry(claude.New(t.TempDir())),
		func(_ context.Context, dir string, argv []string) error {
			gotDir, gotArgv = dir, argv
			return nil
		})

	if err := start(context.Background(), "claude", "/Users/dev/git/api"); err != nil {
		t.Fatalf("start() error = %v, want nil", err)
	}
	if gotDir != "/Users/dev/git/api" {
		t.Errorf("dir = %q, want /Users/dev/git/api", gotDir)
	}
	if !reflect.DeepEqual(gotArgv, []string{"claude"}) {
		t.Errorf("argv = %v, want [claude]", gotArgv)
	}
}

func TestStartSessionRefusesWhatItCannotStart(t *testing.T) {
	tests := []struct {
		name  string
		agent string
		want  string
	}{
		{name: "unregistered", agent: "codex", want: "no provider registered"},
		{name: "not a starter", agent: "readonly", want: "cannot start"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ran := false
			start := startSession(provider.NewRegistry(readOnlyProvider{}),
				func(context.Context, string, []string) error {
					ran = true
					return nil
				})

			err := start(context.Background(), tt.agent, "/Users/dev/git/api")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("start() error = %v, want it to mention %q", err, tt.want)
			}
			if ran {
				t.Error("the terminal was asked to spawn something anyway")
			}
		})
	}
}

func TestApplyConfigTakesTheOverlaySection(t *testing.T) {
	opts := &Options{}
	root := newRootCommand(opts, noopRunners())

	corner := "top-left"
	size := 96
	if err := applyConfig(root, opts, config.File{Overlay: config.Overlay{Corner: corner, Size: &size}}); err != nil {
		t.Fatalf("applyConfig() error = %v", err)
	}

	if opts.Overlay.Corner != corner {
		t.Errorf("Overlay.Corner = %q, want %q", opts.Overlay.Corner, corner)
	}
	if opts.Overlay.Size == nil || *opts.Overlay.Size != 96 {
		t.Errorf("Overlay.Size = %v, want 96", opts.Overlay.Size)
	}
}

// An unknown value names the ones that exist, like every other enumerated
// setting here: a message that only says the value is wrong leaves nothing to
// correct towards.
func TestApplyConfigRejectsAnUnknownCorner(t *testing.T) {
	opts := &Options{}
	root := newRootCommand(opts, noopRunners())

	err := applyConfig(root, opts, config.File{Overlay: config.Overlay{Corner: "middle"}})
	if err == nil {
		t.Fatal("applyConfig() error = nil, want a refusal")
	}
	for _, want := range []string{"middle", "bottom-left", "top-right"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
