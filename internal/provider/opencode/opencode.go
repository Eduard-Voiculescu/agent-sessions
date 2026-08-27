// Package opencode adapts opencode's on-disk state to sessions.
package opencode

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
	"github.com/eduardvoiculescu/agent-sessions/internal/untrusted"
)

type Provider struct {
	dir string
}

// New resolves an empty dir through Dir.
func New(dir string) *Provider {
	return &Provider{dir: cmp.Or(dir, Dir())}
}

// Dir returns opencode's storage directory, honouring $XDG_DATA_HOME the way
// opencode itself does.
func Dir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode", "storage")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "opencode", "storage")
	}
	return filepath.Join(home, ".local", "share", "opencode", "storage")
}

func (p *Provider) Name() string { return "opencode" }

// record is the subset of a session file this reads. Every field below was
// present and non-empty in all 36 records on the machine this was written
// against, which is why none of them needs a fallback the way a Claude Code
// transcript's name does.
type record struct {
	ID        string `json:"id"`
	Directory string `json:"directory"`
	Title     string `json:"title"`
	Time      struct {
		Created int64 `json:"created"`
		Updated int64 `json:"updated"`
	} `json:"time"`
	// ParentID marks a child session spawned by another. Those are conversations
	// nobody held directly, the same shape as Claude Code's sidechain
	// transcripts, and listing them puts rows in the picker that resume into the
	// middle of somebody else's sub-agent.
	ParentID string `json:"parentID"`
}

// Discover reads at most limit sessions, newest first. A missing storage
// directory is not an error: opencode may simply not be installed.
func (p *Provider) Discover(_ context.Context, limit int) ([]session.Session, error) {
	root := filepath.Join(p.dir, "session")
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", root, err)
	}

	var (
		sessions []session.Session
		unread   int
	)
	for _, entry := range entries {
		// One directory per project, named by a hash of the directory. The hash
		// is never reversed: the session record stores its directory outright.
		if !entry.IsDir() {
			continue
		}

		project := filepath.Join(root, entry.Name())
		files, err := os.ReadDir(project)
		if err != nil {
			unread++
			continue
		}

		for _, file := range files {
			if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
				continue
			}

			s, ok, err := read(filepath.Join(project, file.Name()))
			if err != nil {
				unread++
				continue
			}
			if ok {
				sessions = append(sessions, s)
			}
		}
	}

	session.Sort(sessions)
	if limit > 0 && len(sessions) > limit {
		sessions = sessions[:limit]
	}

	if unread > 0 {
		return sessions, fmt.Errorf("%d opencode session files could not be read", unread)
	}
	return sessions, nil
}

// read turns one session file into a session. ok is false for a record that is
// readable but not a session anyone opened.
func read(path string) (session.Session, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return session.Session{}, false, err
	}

	var rec record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return session.Session{}, false, fmt.Errorf("parsing %s: %w", path, err)
	}
	if rec.ID == "" {
		return session.Session{}, false, fmt.Errorf("%s has no session id", path)
	}
	if rec.ParentID != "" {
		return session.Session{}, false, nil
	}

	return session.Session{
		Agent: "opencode",
		ID:    rec.ID,
		// Title and Name are the same string: opencode stores a real title, so
		// there is no prompt to salvage a name from and no precedence to apply.
		Title: rec.Title,
		Name:  cmp.Or(rec.Title, rec.ID),
		Cwd:   rec.Directory,
		// GitBranch is deliberately empty: opencode does not record it. The
		// BRANCH column reads "-" and the ticket and pull-request actions do not
		// offer themselves, which is correct — shelling out to git for every row
		// to invent one would be worse.
		Transcript: path,
		LastActive: lastActive(rec, path),
	}, true, nil
}

// lastActive falls back to the file's mtime rather than to the zero time: a
// record with no timestamp would otherwise sort below every session that has
// one, forever, which reads as the session having vanished.
func lastActive(rec record, path string) time.Time {
	if ms := cmp.Or(rec.Time.Updated, rec.Time.Created); ms > 0 {
		return time.UnixMilli(ms)
	}
	if info, err := os.Stat(path); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}

// StartArgv starts opencode fresh. The working directory is the caller's to
// supply, so nothing about the session being new appears in argv.
func (p *Provider) StartArgv() []string { return []string{"opencode"} }

// ResumeArgv builds the resume command. --fork is documented only alongside
// --session or --continue, so it is appended to the session form rather than
// used on its own.
func (p *Provider) ResumeArgv(s session.Session, fork bool) ([]string, error) {
	// The id comes out of the session record, so `--session <id>` would carry
	// whatever wrote that record put there — a dashed value reaches opencode's
	// own parser as a flag rather than as this session.
	if err := untrusted.ArgValue(s.ID); err != nil {
		return nil, fmt.Errorf("refusing to resume this opencode session: %w", err)
	}

	argv := []string{"opencode", "--session", s.ID}
	if fork {
		argv = append(argv, "--fork")
	}
	return argv, nil
}
