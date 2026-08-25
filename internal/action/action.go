// Package action defines the operations the picker can run against a session.
package action

import (
	"context"
	"os/exec"
	"slices"
	"time"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

// Group buckets an action in the palette. The action names its own bucket
// because only the action knows what it does; the palette knows only how to
// draw the buckets. The constants are declared in the order they are drawn.
type Group int

const (
	// GroupGoTo opens something outside the picker.
	GroupGoTo Group = iota
	// GroupClipboard puts something on the clipboard.
	GroupClipboard
	// GroupDanger is destructive or otherwise not freely undone.
	GroupDanger
	// GroupPicker acts on the picker rather than on any session.
	GroupPicker
)

// Groups is every group in drawing order.
func Groups() []Group {
	return []Group{GroupGoTo, GroupClipboard, GroupDanger, GroupPicker}
}

func (g Group) String() string {
	switch g {
	case GroupGoTo:
		return "go to"
	case GroupClipboard:
		return "clipboard"
	case GroupDanger:
		return "danger"
	default:
		return "picker"
	}
}

// Action is one entry in the palette.
type Action interface {
	ID() string
	Group() Group
	Label(s session.Session) string
	Available(s session.Session) bool
	// Confirm returns the prompt to show first, or "" to run immediately.
	Confirm(s session.Session) string
	Run(ctx context.Context, s session.Session) (Result, error)
}

// Result is what the picker does after an action succeeds.
type Result struct {
	Status  string
	Refresh bool
}

// Contributor is implemented by a provider that adds agent-specific actions.
// It is declared here rather than in the provider package so this package never
// imports one; the wiring layer performs the type assertion.
type Contributor interface {
	Actions() []Action
}

// Config carries the side effects and settings the built-in actions need. Every
// function field has a real default; tests replace them.
type Config struct {
	Workspace string
	Prefixes  []string
	// Provider is the tracker the ticket action opens against. A zero value
	// means the owner said nothing, which resolves to Linear: the flags this
	// replaces defaulted there, and a config file that omits the key must not
	// silently drop the entry.
	Provider Provider
	Alive    func(pid int) bool
	// Elapsed reports how long the process holding a pid has been running, so a
	// pid the registry named can be told from a later process that inherited the
	// number.
	Elapsed   func(pid int) (time.Duration, error)
	Signal    func(pid int, sig int) error
	Clipboard func(text string) error
	OpenURL   func(url string) error
	// ResumeCommand renders the shell command that re-enters a session. It is
	// injected because only the wiring layer may ask the session's agent how it
	// is resumed; a hardcoded one here would copy a plausible but wrong command
	// for every agent but Claude Code.
	ResumeCommand func(s session.Session) (string, error)
	Contribute    func(agent string) []Action
	// Hidden is the action ids the owner asked not to see.
	Hidden []string
	// LookPath resolves an external tool. It is called once, when the registry
	// is built, so an action's Available stays a pure check rather than a PATH
	// lookup on every frame the palette draws.
	LookPath func(string) (string, error)
	// Editor and VCS are the tools the owner configured for those categories. A
	// zero value means nothing was configured, and detection picks the first
	// installed tool from the category's table instead.
	Editor Tool
	VCS    Tool
	// Run launches an external tool. The tools launched here are CLI helpers
	// that hand off to a GUI app and exit immediately, so this waits for them:
	// a non-zero exit is the only signal that the hand-off failed.
	Run func(ctx context.Context, binary string, args ...string) error
	// Output runs a tool in a directory and returns its standard output. The
	// directory is not optional: gh works out which repository it is talking
	// about from the working directory, and the picker's own is not the
	// session's.
	Output func(ctx context.Context, dir, binary string, args ...string) (string, error)
}

// Registry answers which actions apply to a session.
type Registry struct {
	builtins   []Action
	contribute func(agent string) []Action
	hidden     map[string]bool
}

// NewRegistry returns a registry over the built-in actions plus whatever
// Contribute supplies per agent.
func NewRegistry(cfg Config) *Registry {
	if cfg.Alive == nil {
		cfg.Alive = defaultAlive
	}
	if cfg.Signal == nil {
		cfg.Signal = defaultSignal
	}
	if cfg.Elapsed == nil {
		cfg.Elapsed = SystemElapsed
	}
	if cfg.Clipboard == nil {
		cfg.Clipboard = SystemClipboard
	}
	if cfg.LookPath == nil {
		cfg.LookPath = exec.LookPath
	}
	if cfg.Run == nil {
		cfg.Run = SystemRun
	}
	if cfg.Output == nil {
		cfg.Output = SystemOutput
	}
	if cfg.OpenURL == nil {
		cfg.OpenURL = SystemOpen
	}
	if cfg.Provider.IssueURL == nil {
		cfg.Provider, _ = LookupProvider("linear")
	}

	hidden := make(map[string]bool, len(cfg.Hidden))
	for _, id := range cfg.Hidden {
		hidden[id] = true
	}
	return &Registry{builtins: builtins(cfg), contribute: cfg.Contribute, hidden: hidden}
}

// For returns the actions available for s, in palette order.
func (r *Registry) For(s session.Session) []Action {
	candidates := r.builtins
	if r.contribute != nil {
		candidates = append(append([]Action{}, candidates...), r.contribute(s.Agent)...)
	}

	out := make([]Action, 0, len(candidates))
	for _, a := range candidates {
		// Hidden is not the same as unavailable: an unavailable action still
		// appears with its reason, because there is something to correct, while
		// a hidden one is gone because the owner said so.
		if a.Available(s) && !r.hidden[a.ID()] {
			out = append(out, a)
		}
	}
	return out
}

// IDs is every action id this registry can produce for the given agents,
// sorted. It exists so a hide list can be validated against something real
// rather than a list written down twice.
func (r *Registry) IDs(agents []string) []string {
	seen := map[string]bool{}
	for _, a := range r.builtins {
		seen[a.ID()] = true
	}
	if r.contribute != nil {
		for _, agent := range agents {
			for _, a := range r.contribute(agent) {
				seen[a.ID()] = true
			}
		}
	}

	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
