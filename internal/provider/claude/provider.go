// Package claude adapts Claude Code's on-disk state to sessions.
package claude

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/eduardvoiculescu/agent-sessions/internal/action"
	"github.com/eduardvoiculescu/agent-sessions/internal/session"
	"github.com/eduardvoiculescu/agent-sessions/internal/untrusted"
)

type Provider struct {
	dir   string
	alive func(pid int) bool
}

type Option func(*Provider)

func WithAliveFunc(fn func(int) bool) Option {
	return func(p *Provider) { p.alive = fn }
}

// New resolves an empty dir through Dir.
func New(dir string, opts ...Option) *Provider {
	p := &Provider{dir: cmp.Or(dir, Dir()), alive: pidAlive}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Dir returns the Claude Code directory, honouring $CLAUDE_CONFIG_DIR the way
// Claude Code itself does.
func Dir() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

func (p *Provider) Name() string { return "claude" }

// Alive reports whether pid is still running, through the same check the live
// registry filters with, so the palette and the rows it was drawn from cannot
// disagree about what is killable.
func (p *Provider) Alive(pid int) bool { return p.alive(pid) }

func (p *Provider) Live(context.Context) ([]session.Session, error) {
	return readRegistry(p.dir, p.alive)
}

// Discover parses at most limit transcripts, newest first.
func (p *Provider) Discover(_ context.Context, limit int) ([]session.Session, error) {
	found, err := discover(filepath.Join(p.dir, "projects"))
	if err != nil {
		return nil, err
	}

	slices.SortFunc(found, func(a, b transcript) int {
		return b.modTime.Compare(a.modTime)
	})
	if limit > 0 && len(found) > limit {
		found = found[:limit]
	}

	var (
		mu       sync.Mutex
		sessions = make([]session.Session, 0, len(found))
		unread   int
	)

	var group errgroup.Group
	group.SetLimit(runtime.GOMAXPROCS(0))
	for _, tr := range found {
		group.Go(func() error {
			parsed, err := parseTranscript(tr.path)
			if err != nil {
				mu.Lock()
				unread++
				mu.Unlock()
				return nil
			}

			// Subagent transcripts are conversations the user never held and
			// cannot resume; Claude Code stores them beside real sessions.
			if parsed.IsSidechain {
				return nil
			}

			id := cmp.Or(transcriptID(tr.path), parsed.SessionID)
			s := session.Session{
				Agent:      "claude",
				ID:         id,
				Title:      parsed.Title,
				Cwd:        parsed.Cwd,
				GitBranch:  parsed.GitBranch,
				Transcript: tr.path,
				LastActive: tr.modTime,
			}
			s.Name = cmp.Or(oneLine(parsed.Title), oneLine(cleanFirstPrompt(parsed.FirstPrompt)), s.ShortID())

			mu.Lock()
			sessions = append(sessions, s)
			mu.Unlock()
			return nil
		})
	}
	// Every worker returns nil; an unreadable transcript costs one row, not the
	// whole scan, so it is counted rather than propagated.
	group.Wait()

	// Job records are folded in after the scan rather than during it: they are a
	// handful of files against hundreds of transcripts, and a failure to read
	// them must cost the detail column, never the session list.
	jobs, jobErr := readJobs(p.dir)
	applyJobs(sessions, jobs)

	if unread > 0 {
		return sessions, errors.Join(fmt.Errorf("%d of %d transcripts could not be read", unread, len(found)), jobErr)
	}
	return sessions, jobErr
}

func (p *Provider) ResumeArgv(s session.Session, fork bool) ([]string, error) {
	target := cmp.Or(s.ID, s.Transcript)
	if target == "" {
		return nil, errors.New("session has neither an id nor a transcript to resume")
	}
	// The id is a transcript's filename or a field out of its JSON, so it is
	// whatever wrote the file says it is. `--resume` takes its value
	// positionally, which is the parser shape that reads a dashed value as the
	// next flag instead: a transcript named --dangerously-skip-permissions.jsonl
	// would otherwise turn resuming it into passing that.
	if err := untrusted.ArgValue(target); err != nil {
		return nil, fmt.Errorf("refusing to resume %s: %w", s.ShortID(), err)
	}

	argv := []string{"claude", "--resume", target}
	if fork {
		argv = append(argv, "--fork-session")
	}
	return argv, nil
}

// StartArgv starts Claude Code fresh. The working directory is the caller's to
// supply, so nothing about the session being new appears in argv.
func (p *Provider) StartArgv() []string { return []string{"claude"} }

// ResumeWithPromptArgv resumes with prompt as the opening message. The
// separator is not optional: Claude Code takes the prompt positionally, so
// without it a message beginning with a dash is parsed as a flag —
// `claude --resume <id> "-v"` prints the version and never resumes anything.
func (p *Provider) ResumeWithPromptArgv(s session.Session, prompt string) ([]string, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("no message to send")
	}

	argv, err := p.ResumeArgv(s, false)
	if err != nil {
		return nil, err
	}
	return append(argv, "--", prompt), nil
}

func transcriptID(path string) string {
	base := filepath.Base(path)
	return base[:len(base)-len(filepath.Ext(base))]
}

// oneLine collapses whitespace so a name is a name. A first prompt is
// frequently a whole pasted brief, and every consumer of this field — the
// picker's table, `list`, `--json` — wants one line. The renderer flattens
// again as a backstop, since it is the only place where failing to would break
// the frame.
func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// cleanFirstPrompt turns a raw first user message into a name candidate. A
// slash command wraps its expansion in an XML-ish tag before Claude Code ever
// sees it, so the transcript's first "user" line is that wrapper, not what the
// person typed.
func cleanFirstPrompt(prompt string) string {
	trimmed := strings.TrimSpace(prompt)
	if !strings.HasPrefix(trimmed, "<") {
		return prompt
	}

	end := strings.IndexByte(trimmed, '>')
	if end == -1 {
		return prompt
	}
	tag := trimmed[1:end]

	// The caveat block is boilerplate about how the message was generated, never
	// user intent, however short a fragment of it survives.
	if tag == "local-command-caveat" {
		return ""
	}

	body := trimmed[end+1:]
	if closer := "</" + tag + ">"; strings.Contains(body, closer) {
		body = body[:strings.Index(body, closer)]
	}
	return strings.TrimSpace(body)
}

// Actions returns the agent-specific actions for Claude Code sessions. Claude
// Code has no delete subcommand, so removal is a filesystem operation here,
// unlike codex which has one.
func (p *Provider) Actions() []action.Action {
	return []action.Action{
		action.NewTrashDelete(action.TrashConfig{ClaudeDir: p.dir, Alive: p.alive}),
	}
}
