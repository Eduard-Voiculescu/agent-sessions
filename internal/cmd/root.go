// Package cmd wires the command-line surface to the session providers.
package cmd

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/eduardvoiculescu/agent-sessions/internal/action"
	"github.com/eduardvoiculescu/agent-sessions/internal/config"
	"github.com/eduardvoiculescu/agent-sessions/internal/label"
	"github.com/eduardvoiculescu/agent-sessions/internal/provider"
	"github.com/eduardvoiculescu/agent-sessions/internal/provider/claude"
	"github.com/eduardvoiculescu/agent-sessions/internal/resume"
	"github.com/eduardvoiculescu/agent-sessions/internal/session"
	"github.com/eduardvoiculescu/agent-sessions/internal/termjump"
	"github.com/eduardvoiculescu/agent-sessions/internal/ui"
)

type Options struct {
	ClaudeDir      string
	Limit          int
	LiveOnly       bool
	Cwd            string
	Agent          string
	JSON           bool
	Workspace      string
	TicketPrefixes []string

	// Editor, VCS and Provider are resolved from the config file. A zero Tool
	// means nothing was configured and the registry detects instead.
	Editor      action.Tool
	VCS         action.Tool
	Provider    action.Provider
	Hidden      []string
	Providers   []string
	OpencodeDir string
	// Overlay is the attention pet's settings, carried here so `config --json`
	// can publish them resolved. The picker itself never reads them.
	Overlay config.Overlay
	// ConfigPath is where the config was read from, "" when no file exists. It
	// is reported by the config command, which is the only way to answer "which
	// file did you actually read".
	ConfigPath string
	// ConfigSources records where each resolved value came from, keyed by the
	// name the config command prints.
	ConfigSources map[string]string
}

// Command is the palette's global-command type, re-exported so the wiring here
// does not spell out the ui package on every entry.
type Command = ui.Command

type runFunc func(cmd *cobra.Command, opts *Options) error

// runners holds what each command does, so tests can drive the flag parsing
// without reaching the filesystem.
type runners struct {
	pick runFunc
	list runFunc
}

func newRootCommand(opts *Options, run runners) *cobra.Command {
	root := &cobra.Command{
		Use:           "agent-sessions",
		Short:         "Browse and resume coding-agent sessions from anywhere",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run.pick(cmd, opts)
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			file, err := config.Load()
			if err != nil {
				return err
			}
			if err := applyConfig(cmd, opts, file); err != nil {
				return err
			}
			return resolveEnvFallbacks(cmd, opts)
		},
	}

	flags := root.PersistentFlags()
	flags.StringVar(&opts.ClaudeDir, "claude-dir", "", "Claude Code directory (default $CLAUDE_CONFIG_DIR, then ~/.claude)")
	flags.IntVar(&opts.Limit, "limit", 200, "maximum transcripts to parse")
	flags.BoolVar(&opts.LiveOnly, "live", false, "only sessions with a running process")
	flags.StringVar(&opts.Cwd, "cwd", "", "only sessions whose directory has this prefix")
	flags.StringVar(&opts.Agent, "agent", "", "only sessions from one agent (claude, opencode)")

	// Only the picker opens tickets. On the persistent set these would be
	// accepted and silently ignored by list and purge.
	local := root.Flags()
	local.StringVar(&opts.Workspace, "linear-workspace", "", "Linear workspace slug, for opening tickets (default $AGENT_SESSIONS_LINEAR_WORKSPACE)")
	local.StringArrayVar(&opts.TicketPrefixes, "ticket-prefix", nil, "issue-key prefix to recognise in branch names (repeatable) (default $AGENT_SESSIONS_TICKET_PREFIX, comma-separated)")

	root.AddCommand(newListCommand(opts, run.list))
	root.AddCommand(newConfigCommand(opts))
	root.AddCommand(newPurgeCommand(opts))
	root.AddCommand(newWatchCommand(opts))
	root.AddCommand(newJumpCommand(opts, termjump.New().Jump))

	return root
}

// noteSource records where a resolved value came from, which is what the config
// command reports. Guarded because resolveEnvFallbacks is reachable in tests
// that never ran applyConfig.
func (o *Options) noteSource(key, from string) {
	if o.ConfigSources == nil {
		o.ConfigSources = map[string]string{}
	}
	o.ConfigSources[key] = from
}

// applyConfig merges a parsed file beneath the flags already parsed into opts,
// so the chain reads flag > env > config > default. It runs before
// resolveEnvFallbacks, which then overrides anything the environment names and
// the flags left alone.
//
// Every check is Changed, never a zero value: a flag left at its default must
// not beat the config file, and --live=false typed on purpose must beat a config
// that turned it on. Those two are indistinguishable by value.
func applyConfig(cmd *cobra.Command, opts *Options, file config.File) error {
	opts.ConfigPath = file.Path
	opts.ConfigSources = map[string]string{}

	source := opts.noteSource

	if file.Editor != "" {
		tool, ok := action.LookupEditor(file.Editor)
		if !ok {
			return unknownValue(file.Path, "editor", file.Editor, action.EditorNames())
		}
		opts.Editor = tool
		source("editor", "config")
	}

	if file.VCS != "" {
		tool, ok := action.LookupVCS(file.VCS)
		if !ok {
			return unknownValue(file.Path, "vcs", file.VCS, action.VCSNames())
		}
		opts.VCS = tool
		source("vcs", "config")
	}

	if file.Tickets.Provider != "" {
		provider, ok := action.LookupProvider(file.Tickets.Provider)
		if !ok {
			return unknownValue(file.Path, "ticket provider", file.Tickets.Provider, action.ProviderNames())
		}
		opts.Provider = provider
		source("tickets", "config")
	}

	if file.Forge != "" && file.Forge != "github" {
		return unknownValue(file.Path, "forge provider", file.Forge, []string{"github"})
	}

	if len(file.Hidden) > 0 {
		known := actionRegistry(opts, registryFor(opts)).IDs(agentNames())
		for _, id := range file.Hidden {
			if !slices.Contains(known, id) {
				return unknownValue(file.Path, "action id", id, known)
			}
		}
		opts.Hidden = file.Hidden
		source("hidden", "config")
	}

	// Flags() rather than a captured set: --limit, --live and --cwd are
	// persistent, so on a subcommand they are reached through the inherited set
	// and Changed on the root's own would always answer false.
	if cmd.Flags().Changed("limit") {
		source("limit", "flag")
	} else if file.Limit != nil {
		opts.Limit = *file.Limit
		source("limit", "config")
	}

	if cmd.Flags().Changed("live") {
		source("live", "flag")
	} else if file.Live != nil {
		opts.LiveOnly = *file.Live
		source("live", "config")
	}

	if cmd.Flags().Changed("cwd") {
		source("cwd", "flag")
	} else if file.Cwd != "" {
		opts.Cwd = file.Cwd
		source("cwd", "config")
	}

	// The config beats $CLAUDE_CONFIG_DIR rather than losing to it: that variable
	// belongs to Claude Code and is this tool's fallback default, not somebody
	// overriding this tool. Only --claude-dir outranks the file.
	if len(file.Providers) > 0 {
		for _, name := range file.Providers {
			if !slices.Contains(providerNames(), name) {
				return unknownValue(file.Path, "provider", name, providerNames())
			}
		}
		opts.Providers = file.Providers
		source("providers", "config")
	}

	if file.OpencodeDir != "" {
		opts.OpencodeDir = file.OpencodeDir
		source("opencode-dir", "config")
	}

	if cmd.Flags().Changed("agent") {
		source("agent", "flag")
	}

	if cmd.Flags().Changed("claude-dir") {
		source("claude-dir", "flag")
	} else if file.ClaudeDir != "" {
		opts.ClaudeDir = file.ClaudeDir
		source("claude-dir", "config")
	}

	if cmd.Flags().Changed("linear-workspace") {
		source("workspace", "flag")
	} else if file.Tickets.Workspace != "" {
		opts.Workspace = file.Tickets.Workspace
		source("workspace", "config")
	}

	if cmd.Flags().Changed("ticket-prefix") {
		source("prefixes", "flag")
	} else if len(file.Tickets.Prefixes) > 0 {
		opts.TicketPrefixes = file.Tickets.Prefixes
		source("prefixes", "config")
	}

	if file.Overlay.Corner != "" && !slices.Contains(overlayCorners(), file.Overlay.Corner) {
		return unknownValue(file.Path, "overlay corner", file.Overlay.Corner, overlayCorners())
	}
	opts.Overlay = file.Overlay
	// Checked field by field rather than against the zero value: Overlay holds a
	// slice, which is not comparable.
	if file.Overlay.Corner != "" || file.Overlay.Size != nil || file.Overlay.OffsetX != nil ||
		file.Overlay.Sound != nil || len(file.Overlay.Raise) > 0 {
		source("overlay", "config")
	}

	return nil
}

// agentNames is every agent whose contributed actions can be hidden. It is a
// literal until spec 2 lands more providers than Claude Code.
func agentNames() []string { return []string{"claude"} }

// overlayCorners is where the pet may sit, in the order the config file's own
// comment lists them.
func overlayCorners() []string {
	return []string{"bottom-left", "bottom-right", "top-left", "top-right"}
}

// unknownValue names what is valid, because a message that only says the value
// is wrong leaves nothing to correct towards. The file and line come from the
// parser where it has them; a value error is not tied to a line, so the path
// alone is carried.
func unknownValue(path, what, got string, known []string) error {
	where := ""
	if path != "" {
		where = " in " + path
	}
	return fmt.Errorf("unknown %s %q%s: known %ss are %s", what, got, where, what, strings.Join(known, ", "))
}

// resolveEnvFallbacks checks Changed rather than a zero-value default: an
// empty string or nil slice could mean either "unset" or "explicitly set to
// empty", and only Changed tells the two apart.
func resolveEnvFallbacks(cmd *cobra.Command, opts *Options) error {
	if !cmd.Flags().Changed("linear-workspace") {
		if env := os.Getenv("AGENT_SESSIONS_LINEAR_WORKSPACE"); env != "" {
			opts.Workspace = env
			// The source is restamped, not left as the config file's: the value
			// on screen would otherwise name a file it did not come from.
			opts.noteSource("workspace", "env")
		}
	}

	if !cmd.Flags().Changed("ticket-prefix") {
		if env := os.Getenv("AGENT_SESSIONS_TICKET_PREFIX"); env != "" {
			opts.TicketPrefixes = parseTicketPrefixes(env)
			opts.noteSource("prefixes", "env")
		}
	}
	opts.TicketPrefixes = cleanTicketPrefixes(opts.TicketPrefixes)

	return nil
}

func parseTicketPrefixes(input string) []string {
	if input == "" {
		return nil
	}
	return cleanTicketPrefixes(strings.Split(input, ","))
}

// cleanTicketPrefixes runs over every source, flag as well as environment:
// --ticket-prefix "" otherwise reaches the action registry as one empty prefix,
// which its len(prefixes) checks read as configured while it can never match a
// branch, so the ticket entry vanishes with no diagnostic to correct.
func cleanTicketPrefixes(prefixes []string) []string {
	var kept []string
	for _, p := range prefixes {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	return kept
}

func Execute() error {
	var opts Options
	return newRootCommand(&opts, runners{pick: runPicker, list: runList}).Execute()
}

func runPicker(cmd *cobra.Command, opts *Options) error {
	ctx := cmd.Context()

	sessions, loadErr := loadSessions(ctx, opts)
	if len(sessions) == 0 {
		if loadErr != nil {
			return loadErr
		}
		return fmt.Errorf("no sessions found under %s%s", cmp.Or(opts.ClaudeDir, claude.Dir()), filterSuffix(opts))
	}

	registry := registryFor(opts)
	choice, chosen, err := ui.Run(pickerConfigWith(ctx, opts, sessions, loadErr, registry, label.Open()))
	if err != nil {
		return err
	}
	if !chosen {
		return nil
	}

	agent, ok := registry.Find(choice.Session.Agent)
	if !ok {
		return fmt.Errorf("no provider registered for agent %q", choice.Session.Agent)
	}

	argv, err := resumeArgv(agent, choice)
	if err != nil {
		return err
	}

	return resume.New().Run(choice.Session, argv)
}

// resumeArgv picks the argv for the chosen session: a plain resume, or one
// carrying the message composed in the preview. Not every agent can be handed
// an opening prompt, so the capability is asked for rather than assumed.
func resumeArgv(agent provider.Provider, choice ui.Choice) ([]string, error) {
	if choice.Message == "" {
		return agent.ResumeArgv(choice.Session, choice.Fork)
	}

	prompter, ok := agent.(provider.Prompter)
	if !ok {
		return nil, fmt.Errorf("%s cannot be resumed with a message", agent.Name())
	}
	return prompter.ResumeWithPromptArgv(choice.Session, choice.Message)
}

// pickerConfig builds the picker's Config from opts, split out from runPicker
// so the wiring can be tested directly: ui.Run blocks on a real terminal, so a
// test exercises this instead of the picker itself.
func pickerConfig(ctx context.Context, opts *Options, sessions []session.Session, loadErr error, registry *provider.Registry) ui.Config {
	return pickerConfigWith(ctx, opts, sessions, loadErr, registry, label.Open())
}

// pickerConfigWith takes the names store as a parameter so a test can point it at
// a temporary file rather than at the developer's own.
func pickerConfigWith(ctx context.Context, opts *Options, sessions []session.Session, loadErr error, registry *provider.Registry, names *label.Store) ui.Config {
	filter := func(sessions []session.Session) []session.Session {
		return FilterOptions(sessions, opts)
	}

	return ui.Config{
		Sessions: sessions,
		Refresh: func() []session.Session {
			live, _ := registry.Live(ctx)
			// The names are applied to the live set as well as to the baseline: a
			// session renamed before it had written a transcript exists only here,
			// and Merge takes the label from whichever side carries it.
			applyNames(live, names)
			return filter(live)
		},
		Filter:  filter,
		LoadErr: loadErr,
		Actions: actionsFor(opts, registry),
		Reload: func() ([]session.Session, error) {
			return loadSessionsNamed(ctx, opts, names)
		},
		Rename:      renameSession(names),
		Commands:    func() []Command { return globalCommands(opts) },
		ToggleLive:  toggleLive(opts),
		Jump:        termjump.New().Jump,
		Send:        termjump.New().Send,
		StartAgents: registry.Startable(),
		Start:       startSession(registry, termjump.New().Start),
		Preview:     previewFor(registry),
		// Read through accessors, not copied: globalCommands mutates these two on
		// the same opts the Filter closure above reads, so a copy would leave the
		// header block reporting the filters the picker started with.
		LiveOnly:       func() bool { return opts.LiveOnly },
		Cwd:            func() string { return opts.Cwd },
		TicketPrefixes: opts.TicketPrefixes,
		ClaudeDir:      cmp.Or(opts.ClaudeDir, claude.Dir()),
	}
}

// startSession resolves an agent's own start argv and hands it to the terminal.
// spawn is a parameter rather than termjump reached directly, so the resolution
// is testable without an iTerm2 and without moving anybody's windows.
func startSession(registry *provider.Registry, spawn func(ctx context.Context, dir string, argv []string) error) func(context.Context, string, string) error {
	return func(ctx context.Context, agent, dir string) error {
		p, ok := registry.Find(agent)
		if !ok {
			return fmt.Errorf("no provider registered for agent %q", agent)
		}
		starter, ok := p.(provider.Starter)
		if !ok {
			return fmt.Errorf("%s cannot start a new session", agent)
		}
		return spawn(ctx, dir, starter.StartArgv())
	}
}

func actionsFor(opts *Options, registry *provider.Registry) func(session.Session) []action.Action {
	return actionRegistry(opts, registry).For
}

// actionRegistry is the one place the action config is assembled, so the
// registry the picker uses and the one the config command inspects cannot
// disagree about what is offered.
func actionRegistry(opts *Options, registry *provider.Registry) *action.Registry {
	return action.NewRegistry(action.Config{
		Workspace: opts.Workspace,
		Prefixes:  opts.TicketPrefixes,
		Provider:  opts.Provider,
		Editor:    opts.Editor,
		VCS:       opts.VCS,
		Hidden:    opts.Hidden,
		Alive:     aliveCheck(registry),
		ResumeCommand: func(s session.Session) (string, error) {
			return resumeCommand(registry, s)
		},
		Contribute: func(agent string) []action.Action {
			p, ok := registry.Find(agent)
			if !ok {
				return nil
			}
			contributor, ok := p.(action.Contributor)
			if !ok {
				return nil
			}
			return contributor.Actions()
		},
	})
}

// liveChecker is implemented by a provider that can say whether a pid is still
// running.
type liveChecker interface {
	Alive(pid int) bool
}

// previewer is implemented by a provider that can read a session's transcript
// back for the picker's preview.
type previewer interface {
	Preview(s session.Session) ([]session.Message, error)
}

// previewFor looks the session's own provider up by agent rather than
// hardcoding claude.Preview: a session from a future provider with no preview
// support gets a clear error instead of being parsed as someone else's
// transcript format.
func previewFor(registry *provider.Registry) func(session.Session) ([]session.Message, error) {
	return func(s session.Session) ([]session.Message, error) {
		p, ok := registry.Find(s.Agent)
		if !ok {
			return nil, fmt.Errorf("no provider registered for agent %q", s.Agent)
		}
		preview, ok := p.(previewer)
		if !ok {
			return nil, fmt.Errorf("%s sessions have no preview available", s.Agent)
		}
		return preview.Preview(s)
	}
}

// aliveCheck borrows a provider's liveness check so the palette's kill entry
// agrees with the rows the picker drew. A pid is a pid, so the first provider
// publishing one answers for every agent; a nil result leaves the action
// package on its own default.
func aliveCheck(registry *provider.Registry) func(int) bool {
	for _, p := range registry.Providers() {
		if checker, ok := p.(liveChecker); ok {
			return checker.Alive
		}
	}
	return nil
}

// resumeCommand renders a session's resume argv as one pasteable shell command.
// Every element is quoted, because an agent may take an argument that a shell
// would otherwise split or expand.
func resumeCommand(registry *provider.Registry, s session.Session) (string, error) {
	p, ok := registry.Find(s.Agent)
	if !ok {
		return "", fmt.Errorf("no provider registered for agent %q", s.Agent)
	}

	argv, err := p.ResumeArgv(s, false)
	if err != nil {
		return "", fmt.Errorf("building the resume command for %s: %w", s.Agent, err)
	}

	quoted := make([]string, 0, len(argv))
	for _, arg := range argv {
		quoted = append(quoted, action.ShellQuote(arg))
	}
	command := strings.Join(quoted, " ")
	if s.Cwd == "" {
		return command, nil
	}
	return "cd " + action.ShellQuote(s.Cwd) + " && " + command, nil
}

func globalCommands(opts *Options) []Command {
	cmds := []Command{
		{Label: "toggle --live filter", Run: toggleLive(opts)},
		{Label: "reload sessions", Run: func() (action.Result, error) {
			return action.Result{Status: "reloaded"}, nil
		}},
	}
	if opts.Cwd != "" {
		cmds = append(cmds, Command{Label: "clear --cwd filter", Run: func() (action.Result, error) {
			opts.Cwd = ""
			return action.Result{Status: "cleared --cwd"}, nil
		}})
	}
	return cmds
}

// toggleLive is shared by the palette command and the ctrl+h binding, so the
// two cannot drift into flipping the filter differently.
func toggleLive(opts *Options) func() (action.Result, error) {
	return func() (action.Result, error) {
		opts.LiveOnly = !opts.LiveOnly
		return action.Result{Status: fmt.Sprintf("--live %v", opts.LiveOnly)}, nil
	}
}

func filterSuffix(opts *Options) string {
	var active []string
	if opts.LiveOnly {
		active = append(active, "--live")
	}
	if opts.Cwd != "" {
		active = append(active, fmt.Sprintf("--cwd %q", opts.Cwd))
	}
	if opts.Agent != "" {
		active = append(active, fmt.Sprintf("--agent %q", opts.Agent))
	}
	if len(active) == 0 {
		return ""
	}
	return " matching " + strings.Join(active, " and ")
}
