// Package label keeps the names an owner gave their sessions.
//
// The names live in a file this program owns rather than in an agent's own
// state. Writing a name into Claude Code's session registry would put it where
// Claude Code rewrites at will, would exist only while the process is live — so a
// finished session could never be renamed — and would break the rule the rest of
// this codebase holds to: an agent's state is read, never written.
package label

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/eduardvoiculescu/agent-sessions/internal/config"
	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

// MaxLength is the longest name accepted. The name column is a few dozen cells
// wide and every row is truncated to it, so a name past this is one nobody can
// read back — refusing it is kinder than storing something that only ever shows
// as an ellipsis.
const MaxLength = 120

// Store is the names file. Nothing is cached: the file holds a handful of short
// strings, and a stale map would show a rename that had not happened or hide one
// that had.
type Store struct {
	path string
}

func New(path string) *Store {
	return &Store{path: path}
}

// Path is where the names live: beside the config file, so one directory holds
// everything this program owns. It is "" when no location can be determined, and
// a Store built on that reads empty and refuses to write.
func Path() string {
	dir := filepath.Dir(config.Path())
	if dir == "" || dir == "." {
		return ""
	}
	return filepath.Join(dir, "names.json")
}

func Open() *Store {
	return New(Path())
}

// Names is every name that has been set, keyed the way sessions are: two agents
// may mint the same id, so an id alone identifies nothing.
//
// A missing file is not an error. A machine that has renamed nothing is the
// common case, and the picker must open on it.
func (s *Store) Names() (map[session.Key]string, error) {
	if s.path == "" {
		return map[session.Key]string{}, nil
	}

	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[session.Key]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.path, err)
	}

	var stored map[string]string
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.path, err)
	}

	names := make(map[session.Key]string, len(stored))
	for encoded, name := range stored {
		agent, id, found := strings.Cut(encoded, ":")
		if !found || agent == "" || id == "" {
			continue
		}
		names[session.Key{Agent: agent, ID: id}] = name
	}
	return names, nil
}

func (s *Store) Set(key session.Key, name string) error {
	clean, err := Clean(name)
	if err != nil {
		return err
	}
	return s.rewrite(func(stored map[string]string) {
		stored[encode(key)] = clean
	})
}

// Clear forgets one name. Clearing something never named is not an error: it is
// the palette offering the entry against a session whose name has since been
// cleared elsewhere, and refusing would be pedantry.
func (s *Store) Clear(key session.Key) error {
	return s.rewrite(func(stored map[string]string) {
		delete(stored, encode(key))
	})
}

// Clean is the whole of what a name may be. It is checked here, where a name is
// typed, rather than at each of the places it is later drawn: this file is read
// back onto a terminal, and a value that a terminal reads as a command must never
// reach one.
func Clean(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.New("a name cannot be empty")
	}
	if i := strings.IndexFunc(trimmed, unicode.IsControl); i >= 0 {
		return "", fmt.Errorf("a name cannot contain a control character (at byte %d)", i)
	}
	if len([]rune(trimmed)) > MaxLength {
		return "", fmt.Errorf("a name cannot be longer than %d characters", MaxLength)
	}
	return trimmed, nil
}

// Apply writes the names onto the sessions that have one, in place. Both fields
// are set: Label is what Merge reads to keep the name ahead of every derived one,
// and Name is what every row already draws.
func Apply(sessions []session.Session, names map[session.Key]string) {
	if len(names) == 0 {
		return
	}
	for i := range sessions {
		if name, found := names[sessions[i].Key()]; found {
			sessions[i].Label = name
			sessions[i].Name = name
		}
	}
}

// rewrite reads, changes and replaces the file as one operation, through a
// temporary in the same directory: half of a rewritten file is worse than none,
// and a rename within a directory is the only atomic write there is.
func (s *Store) rewrite(change func(map[string]string)) error {
	if s.path == "" {
		return errors.New("cannot determine where to keep session names")
	}

	stored, err := s.raw()
	if err != nil {
		return err
	}
	change(stored)

	encoded, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding session names: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	temporary, err := os.CreateTemp(dir, "names-*.json")
	if err != nil {
		return fmt.Errorf("writing %s: %w", s.path, err)
	}
	// Removed on every failure below, so a crashed write leaves the previous file
	// standing and no litter beside it.
	defer os.Remove(temporary.Name())

	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		temporary.Close()
		return fmt.Errorf("writing %s: %w", s.path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", s.path, err)
	}
	if err := os.Chmod(temporary.Name(), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", s.path, err)
	}
	if err := os.Rename(temporary.Name(), s.path); err != nil {
		return fmt.Errorf("replacing %s: %w", s.path, err)
	}
	return nil
}

func (s *Store) raw() (map[string]string, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.path, err)
	}

	stored := map[string]string{}
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.path, err)
	}
	return stored, nil
}

func encode(key session.Key) string {
	return key.Agent + ":" + key.ID
}
