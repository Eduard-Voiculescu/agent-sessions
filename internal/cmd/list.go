package cmd

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/eduardvoiculescu/agent-sessions/internal/label"
	"github.com/eduardvoiculescu/agent-sessions/internal/provider"
	"github.com/eduardvoiculescu/agent-sessions/internal/provider/claude"
	"github.com/eduardvoiculescu/agent-sessions/internal/provider/opencode"
	"github.com/eduardvoiculescu/agent-sessions/internal/session"
	"github.com/eduardvoiculescu/agent-sessions/internal/untrusted"
)

func newListCommand(opts *Options, run runFunc) *cobra.Command {
	list := &cobra.Command{
		Use:           "list",
		Short:         "Print sessions without entering the picker",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, opts)
		},
	}
	list.Flags().BoolVar(&opts.JSON, "json", false, "emit JSON instead of a table")

	return list
}

func FilterOptions(sessions []session.Session, opts *Options) []session.Session {
	kept := make([]session.Session, 0, len(sessions))
	for _, s := range sessions {
		if opts.Agent != "" && s.Agent != opts.Agent {
			continue
		}
		if opts.LiveOnly && !s.Live {
			continue
		}
		if opts.Cwd != "" && !strings.HasPrefix(s.Cwd, opts.Cwd) {
			continue
		}
		kept = append(kept, s)
	}
	return kept
}

// providerNames is every agent this binary can read, sorted, for validating the
// config's enable list against something real rather than a list written twice.
func providerNames() []string { return []string{"claude", "opencode"} }

// registryFor builds the enabled providers. An empty Providers means every one
// of them: a fresh install with no config lists every agent it can read, and the
// config file is for narrowing that rather than for making it work.
func registryFor(opts *Options) *provider.Registry {
	var providers []provider.Provider
	for _, name := range providerNames() {
		if len(opts.Providers) > 0 && !slices.Contains(opts.Providers, name) {
			continue
		}
		switch name {
		case "claude":
			providers = append(providers, claude.New(opts.ClaudeDir))
		case "opencode":
			providers = append(providers, opencode.New(opts.OpencodeDir))
		}
	}
	return provider.NewRegistry(providers...)
}

func loadSessions(ctx context.Context, opts *Options) ([]session.Session, error) {
	return loadSessionsNamed(ctx, opts, label.Open())
}

// loadSessionsNamed is loadSessions with the names store injected, so a test can
// point it at a temporary file. Every path that loads sessions goes through here:
// a rename that only reached the picker would vanish from list and from watch.
func loadSessionsNamed(ctx context.Context, opts *Options, names *label.Store) ([]session.Session, error) {
	sessions, err := registryFor(opts).Sessions(ctx, opts.Limit)
	applyNames(sessions, names)
	return FilterOptions(sessions, opts), err
}

// applyNames overlays the owner's own names. A store that cannot be read is
// ignored on purpose: the names are a convenience and the sessions are the point,
// so an unreadable file must not empty the picker.
func applyNames(sessions []session.Session, names *label.Store) {
	if names == nil {
		return
	}
	stored, err := names.Names()
	if err != nil {
		return
	}
	label.Apply(sessions, stored)
}

// renameSession records a name against a session, or clears it when the name is
// empty — which is how the picker's emptied prompt takes a custom name off.
func renameSession(names *label.Store) func(session.Session, string) error {
	return func(s session.Session, name string) error {
		if strings.TrimSpace(name) == "" {
			return names.Clear(s.Key())
		}
		return names.Set(s.Key(), name)
	}
}

// listStatus mirrors the picker's own precedence: a live registry status first,
// then a background agent's own record, which is the only thing that can speak
// for an agent running under the daemon rather than on a tty.
func listStatus(s session.Session) string {
	switch {
	case s.Live:
		return cmp.Or(s.Status, "live")
	case s.JobState != "":
		return s.JobState
	default:
		return "-"
	}
}

func runList(cmd *cobra.Command, opts *Options) error {
	sessions, loadErr := loadSessions(cmd.Context(), opts)

	if opts.JSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(sessions); err != nil {
			return fmt.Errorf("encoding sessions: %w", err)
		}
		return loadErr
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "AGENT\tSTATUS\tNAME\tBRANCH\tDIRECTORY\tAGE")
	// This table goes to the same terminal the picker draws on, so the values
	// need the same neutralising the picker gives them. The JSON branch above is
	// left alone: the encoder escapes a control character on the way out, where
	// it is inert, and a consumer may legitimately want it back.
	for _, s := range untrusted.Sessions(sessions) {
		status := listStatus(s)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Agent, status, cell(s.Name, 60), cell(cmp.Or(s.GitBranch, "-"), 40),
			cell(cmp.Or(s.Cwd, "-"), 60), session.Age(s.LastActive))
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("writing table: %w", err)
	}

	return loadErr
}

// cell collapses whitespace before writing a tabwriter field. A first prompt
// used as a session name can span many lines, and tabwriter is line-oriented:
// one embedded newline splits the row and corrupts every column after it.
func cell(v string, width int) string {
	if width <= 0 {
		return ""
	}
	flat := strings.Join(strings.Fields(v), " ")
	runes := []rune(flat)
	if len(runes) <= width {
		return flat
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}
