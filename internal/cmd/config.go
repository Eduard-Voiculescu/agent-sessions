package cmd

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/eduardvoiculescu/agent-sessions/internal/action"
	"github.com/eduardvoiculescu/agent-sessions/internal/config"
	"github.com/eduardvoiculescu/agent-sessions/internal/provider/claude"
	"github.com/eduardvoiculescu/agent-sessions/internal/provider/opencode"
)

type configOptions struct {
	initialise bool
	force      bool
	json       bool
}

func newConfigCommand(opts *Options) *cobra.Command {
	var local configOptions

	cmd := &cobra.Command{
		Use:           "config",
		Short:         "Show the resolved configuration, or write a starter file",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if local.initialise {
				path := config.Path()
				if path == "" {
					return errors.New("cannot determine where to write a config file")
				}
				if err := writeStarter(path, local.force); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
				return nil
			}
			if local.json {
				return renderConfigJSON(cmd, opts)
			}
			return renderConfig(cmd, opts)
		},
	}

	cmd.Flags().BoolVar(&local.initialise, "init", false, "write a commented starter config file")
	cmd.Flags().BoolVar(&local.force, "force", false, "overwrite an existing file when used with --init")
	cmd.Flags().BoolVar(&local.json, "json", false, "emit the resolved configuration as JSON")

	return cmd
}

// renderConfig answers the question an owner actually has when an action is
// missing: what was read, and where did each value come from.
func renderConfig(cmd *cobra.Command, opts *Options) error {
	out := cmd.OutOrStdout()

	path := opts.ConfigPath
	if path == "" {
		path = "none (" + orDash(config.Path(), "no location") + " does not exist)"
	}
	fmt.Fprintf(out, "file      %s\n\n", path)

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	registry := registryFor(opts)
	reg := actionRegistry(opts, registry)

	editor := resolveTool(opts.Editor, action.Editors(), opts, "editor")
	vcs := resolveTool(opts.VCS, action.VCSClients(), opts, "vcs")

	fmt.Fprintf(w, "editor\t%s\t%s\n", editor.value, editor.source)
	fmt.Fprintf(w, "vcs\t%s\t%s\n", vcs.value, vcs.source)
	fmt.Fprintf(w, "tickets\t%s\t%s\n", ticketSummary(opts), source(opts, "tickets", "default"))
	fmt.Fprintf(w, "workspace\t%s\t%s\n", orDash(opts.Workspace, "—"), source(opts, "workspace", "unset"))
	fmt.Fprintf(w, "prefixes\t%s\t%s\n", orDash(strings.Join(opts.TicketPrefixes, ", "), "—"), source(opts, "prefixes", "unset"))
	fmt.Fprintf(w, "hidden\t%s\t%s\n", orDash(strings.Join(opts.Hidden, ", "), "—"), source(opts, "hidden", "unset"))
	fmt.Fprintf(w, "limit\t%d\t%s\n", opts.Limit, source(opts, "limit", "default"))
	fmt.Fprintf(w, "live\t%v\t%s\n", opts.LiveOnly, source(opts, "live", "default"))
	fmt.Fprintf(w, "cwd\t%s\t%s\n", orDash(opts.Cwd, "—"), source(opts, "cwd", "unset"))
	fmt.Fprintf(w, "claude dir\t%s\t%s\n", orDash(opts.ClaudeDir, claude.Dir()+" (fallback)"), source(opts, "claude-dir", "default"))
	fmt.Fprintf(w, "providers\t%s\t%s\n", orDash(strings.Join(opts.Providers, ", "), strings.Join(providerNames(), ", ")), source(opts, "providers", "all"))
	fmt.Fprintf(w, "opencode dir\t%s\t%s\n", orDash(opts.OpencodeDir, opencode.Dir()+" (fallback)"), source(opts, "opencode-dir", "default"))
	if err := w.Flush(); err != nil {
		return fmt.Errorf("writing configuration: %w", err)
	}

	fmt.Fprintln(out)
	return renderActions(out, reg, opts)
}

// renderActions lists every action and whether it is drawn, because "why is
// this entry not in my palette" is the other question the command exists for.
func renderActions(out io.Writer, reg *action.Registry, opts *Options) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "actions\tID\tSTATE")
	for _, id := range reg.IDs(agentNames()) {
		state := "shown"
		if slices.Contains(opts.Hidden, id) {
			state = "hidden by config"
		}
		fmt.Fprintf(w, "\t%s\t%s\n", id, state)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("writing actions: %w", err)
	}
	return nil
}

type resolved struct {
	value  string
	source string
}

// resolveTool reports the tool that will actually be used and how it was
// arrived at. Detection is repeated here rather than read back from the
// registry: the registry keeps no record of which table row it settled on, and
// naming a tool the picker is not using would be worse than not naming one.
func resolveTool(configured action.Tool, table []action.Tool, opts *Options, key string) resolved {
	if configured.Binary != "" {
		return resolved{value: configured.Name, source: source(opts, key, "config")}
	}
	for _, tool := range table {
		if _, err := exec.LookPath(tool.Binary); err == nil {
			return resolved{value: tool.Name, source: "(detected)"}
		}
	}
	return resolved{value: "—", source: "(none installed)"}
}

func ticketSummary(opts *Options) string {
	if opts.Provider.Name != "" {
		return opts.Provider.Name
	}
	return "linear"
}

// source names where a value came from, which is the question an owner has when
// an action is missing. Parenthesised so the column reads as annotation rather
// than as another value.
func source(opts *Options, key, fallback string) string {
	if from, ok := opts.ConfigSources[key]; ok {
		return "(" + from + ")"
	}
	return "(" + fallback + ")"
}

func orDash(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// resolvedConfig is the whole configuration as one document, for a consumer that
// is not a person: the attention pet reads its own settings from here rather than
// parsing this file's format in another language.
type resolvedConfig struct {
	File        string            `json:"file"`
	Editor      string            `json:"editor"`
	VCS         string            `json:"vcs"`
	Tickets     resolvedTickets   `json:"tickets"`
	Hidden      []string          `json:"hidden"`
	Limit       int               `json:"limit"`
	Live        bool              `json:"live"`
	Cwd         string            `json:"cwd"`
	ClaudeDir   string            `json:"claudeDir"`
	OpencodeDir string            `json:"opencodeDir"`
	Providers   []string          `json:"providers"`
	Overlay     resolvedOverlay   `json:"overlay"`
	Sources     map[string]string `json:"sources"`
}

type resolvedTickets struct {
	Provider  string   `json:"provider"`
	Workspace string   `json:"workspace"`
	Prefixes  []string `json:"prefixes"`
}

type resolvedOverlay struct {
	Corner string   `json:"corner"`
	Offset [2]int   `json:"offset"`
	Size   int      `json:"size"`
	Raise  []string `json:"raise"`
	Sound  bool     `json:"sound"`
}

// The pet's defaults live here rather than in its own language, so one place
// answers "where does it sit and how big is it" for both halves of this program.
func overlayDefaults() resolvedOverlay {
	return resolvedOverlay{
		Corner: "bottom-left",
		Offset: [2]int{24, 24},
		Size:   72,
		Raise:  []string{"waiting"},
	}
}

func resolveOverlay(overlay config.Overlay) resolvedOverlay {
	out := overlayDefaults()
	if overlay.Corner != "" {
		out.Corner = overlay.Corner
	}
	if overlay.OffsetX != nil {
		out.Offset[0] = *overlay.OffsetX
	}
	if overlay.OffsetY != nil {
		out.Offset[1] = *overlay.OffsetY
	}
	if overlay.Size != nil {
		out.Size = *overlay.Size
	}
	if len(overlay.Raise) > 0 {
		out.Raise = overlay.Raise
	}
	if overlay.Sound != nil {
		out.Sound = *overlay.Sound
	}
	return out
}

func renderConfigJSON(cmd *cobra.Command, opts *Options) error {
	// Not cmp.Or: a slice is not comparable, so the empty case is spelt out.
	providers := opts.Providers
	if len(providers) == 0 {
		providers = providerNames()
	}

	document := resolvedConfig{
		File:   opts.ConfigPath,
		Editor: opts.Editor.Name,
		VCS:    opts.VCS.Name,
		Tickets: resolvedTickets{
			Provider:  ticketSummary(opts),
			Workspace: opts.Workspace,
			Prefixes:  opts.TicketPrefixes,
		},
		Hidden:      opts.Hidden,
		Limit:       opts.Limit,
		Live:        opts.LiveOnly,
		Cwd:         opts.Cwd,
		ClaudeDir:   cmp.Or(opts.ClaudeDir, claude.Dir()),
		OpencodeDir: cmp.Or(opts.OpencodeDir, opencode.Dir()),
		Providers:   providers,
		Overlay:     resolveOverlay(opts.Overlay),
		Sources:     opts.ConfigSources,
	}

	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return fmt.Errorf("encoding the configuration: %w", err)
	}
	return nil
}

// starter is written commented out throughout: a starter file that turned
// settings on by writing them would change behaviour the moment it was created,
// which is not what "show me the shape of this file" should do.
const starter = `# agent-sessions configuration.
# Every section and key is optional. Uncomment what you want to change.
#
# Values not set here fall back to detection (for editor and vcs) or to a
# built-in default. A command-line flag always wins over this file.

# [editor]
# default = vscode          # cursor | intellij | sublime | vscode | zed

# [vcs]
# default = fork            # fork | gitkraken | sourcetree | tower

# [tickets]
# provider  = linear
# workspace = your-workspace
# prefixes  = eng, pay

# [forge]
# provider = github

# [actions]
# hide = copy.transcript, process.kill

# [list]
# limit = 200               # transcripts to parse; 0 means no limit

# [filters]
# live = false              # start showing only sessions with a process
# cwd  = ~/git              # start filtered to one directory tree

# [claude]
# dir = ~/.claude           # where Claude Code keeps its state

# [providers]
# enable = claude, opencode # omit to read every agent this binary knows

# [opencode]
# dir = ~/.local/share/opencode/storage

# [overlay]
# corner = bottom-left     # bottom-left | bottom-right | top-left | top-right
# offset = 24,24           # points from that corner, x,y
# size   = 72              # sprite box, points
# raise  = waiting         # status classes that raise the pet
# sound  = false
`

func writeStarter(path string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists; pass --force to overwrite it", path)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("checking %s: %w", path, err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(starter), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
