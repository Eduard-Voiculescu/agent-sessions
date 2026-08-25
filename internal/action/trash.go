package action

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

const trashDirName = ".agent-sessions-trash"

// maxTrashSlots bounds the walk past occupied names, so a trash directory that
// cannot be written to is reported rather than walked forever.
const maxTrashSlots = 1000

// trashFileMode and trashDirMode keep the trash to its owner. A transcript is a
// whole conversation — pasted credentials, file contents, tool output — and
// os.Rename carries the source's mode across, so the mode is set after the move
// rather than inherited from wherever the transcript happened to live.
const (
	trashFileMode = 0o600
	trashDirMode  = 0o700
)

// TrashRoot is where deleted transcripts are kept until purged.
func TrashRoot(claudeDir string) string {
	return filepath.Join(claudeDir, trashDirName)
}

// TrashConfig carries the filesystem operations the delete action needs.
type TrashConfig struct {
	ClaudeDir string
	Rename    func(old, new string) error
	Copy      func(old, new string) error
	Remove    func(path string) error
	Chtimes   func(path string, atime, mtime time.Time) error
	Alive     func(pid int) bool
}

// NewTrashDelete returns the delete action for an agent whose sessions are
// plain transcript files.
func NewTrashDelete(cfg TrashConfig) Action {
	if cfg.Rename == nil {
		cfg.Rename = os.Rename
	}
	if cfg.Copy == nil {
		cfg.Copy = copyFile
	}
	if cfg.Remove == nil {
		cfg.Remove = os.Remove
	}
	if cfg.Chtimes == nil {
		cfg.Chtimes = os.Chtimes
	}
	if cfg.Alive == nil {
		cfg.Alive = defaultAlive
	}
	return trashAction{cfg: cfg}
}

type trashAction struct {
	cfg TrashConfig
}

func (a trashAction) ID() string                   { return "session.delete" }
func (a trashAction) Group() Group                 { return GroupDanger }
func (a trashAction) Label(session.Session) string { return "delete session (move to trash)" }

// Available excludes a live session because Run refuses one: offering the entry
// would spend a confirmation on a guaranteed failure.
func (a trashAction) Available(s session.Session) bool {
	return s.Transcript != "" && !(s.Live && s.PID > 0 && a.cfg.Alive(s.PID))
}

func (a trashAction) Confirm(s session.Session) string {
	return fmt.Sprintf("Move %s's transcript to trash?", s.Name)
}

func (a trashAction) Run(_ context.Context, s session.Session) (Result, error) {
	if s.Live && s.PID > 0 && a.cfg.Alive(s.PID) {
		return Result{}, fmt.Errorf("%s is still running as pid %d; kill it first", s.Name, s.PID)
	}

	target, err := a.target(s.Transcript)
	if err != nil {
		return Result{}, err
	}

	if err := a.move(s.Transcript, target); err != nil {
		return Result{}, err
	}

	// The footer is capped at the row table's span, so a full mv -i command with
	// two absolute paths is never readable there; purge's listing prints it
	// instead, where there is a whole line per file.
	status := fmt.Sprintf("trashed %s; run 'agent-sessions purge' to list trashed files", filepath.Base(target))
	if err := errors.Join(os.Chmod(target, trashFileMode), a.stamp(target)); err != nil {
		// The transcript has already moved. Failing the action now would leave
		// the deleted row on the list with no word of where its file went, so
		// the stamp's failure is reported alongside the success instead — and it
		// leads, because the footer truncates at the row table's width and the
		// reassurance is what can afford to be cut.
		status = fmt.Sprintf("WARNING: %s may be purged early or left readable: %s", filepath.Base(target), err)
	}
	return Result{Status: status, Refresh: true}, nil
}

// stamp records when the file entered the trash. os.Rename preserves the
// transcript's mtime, and an age in the trash that meant time since last
// activity rather than time since deletion would let `purge --older-than`
// destroy a months-idle session the instant it was deleted.
func (a trashAction) stamp(target string) error {
	now := time.Now()
	if err := a.cfg.Chtimes(target, now, now); err != nil {
		return fmt.Errorf("could not stamp deletion time: %w", err)
	}
	return nil
}

// RestoreCommand is the shell command that undoes one trashed transcript. The
// destination is the project directory rather than a file path because a second
// delete of the same session is parked at <id>-2.jsonl, and which of the two
// that was cannot be recovered from the trash alone. -i, because the
// original name may have been taken again since; without it a restore would
// silently clobber whatever holds it now.
func RestoreCommand(claudeDir, trashed string) string {
	project := filepath.Base(filepath.Dir(trashed))
	destination := filepath.Join(claudeDir, "projects", project) + string(filepath.Separator)
	return "mv -i " + ShellQuote(trashed) + " " + ShellQuote(destination)
}

// target mirrors the transcript's project directory inside the trash, because
// two sessions in different repositories can share a filename and a flat trash
// would let one overwrite the other.
func (a trashAction) target(transcript string) (string, error) {
	project := filepath.Base(filepath.Dir(transcript))
	if project == "" || project == "." || project == string(filepath.Separator) {
		return "", fmt.Errorf("cannot derive a trash path for %s", transcript)
	}
	base := filepath.Join(TrashRoot(a.cfg.ClaudeDir), project, filepath.Base(transcript))
	if err := os.MkdirAll(filepath.Dir(base), trashDirMode); err != nil {
		return "", fmt.Errorf("creating trash directory: %w", err)
	}
	return a.claim(base)
}

// claim reserves the trash name before anything is moved onto it. Stat-then-
// rename left a window in which a second delete of the same session could take
// the name first, and os.Rename would then replace that file without a word — in
// the one directory a regretted delete can be recovered from.
//
// The extension is kept on the walk-past names because purge lists by it.
func (a trashAction) claim(base string) (string, error) {
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	for n := 1; n <= maxTrashSlots; n++ {
		candidate := base
		if n > 1 {
			candidate = fmt.Sprintf("%s-%d%s", stem, n, ext)
		}

		file, err := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, trashFileMode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("claiming %s: %w", candidate, err)
		}
		file.Close()
		return candidate, nil
	}
	return "", fmt.Errorf("the trash already holds %d copies of %s", maxTrashSlots, filepath.Base(base))
}

func (a trashAction) move(from, to string) error {
	err := a.cfg.Rename(from, to)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return fmt.Errorf("moving %s to trash: %w", from, err)
	}

	if copyErr := a.cfg.Copy(from, to); copyErr != nil {
		if rmErr := a.cfg.Remove(to); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return fmt.Errorf("copying %s to trash: %w (and the partial copy could not be removed: %v)", from, copyErr, rmErr)
		}
		return fmt.Errorf("copying %s to trash: %w", from, copyErr)
	}
	if err := a.cfg.Remove(from); err != nil {
		return fmt.Errorf("removing %s after copying it to trash: %w", from, err)
	}
	return nil
}

func copyFile(from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return fmt.Errorf("opening %s: %w", from, err)
	}
	defer src.Close()

	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, trashFileMode)
	if err != nil {
		return fmt.Errorf("creating %s: %w", to, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copying to %s: %w", to, err)
	}
	return dst.Sync()
}

// TrashedFile is one transcript awaiting purge. ModTime is when the delete
// action stamped it, so it is time since deletion rather than the session's own
// age.
type TrashedFile struct {
	Path    string
	ModTime time.Time
}

// TrashedFiles lists what is currently in the trash.
func TrashedFiles(claudeDir string) ([]TrashedFile, error) {
	root := TrashRoot(claudeDir)

	var (
		found    []TrashedFile
		problems []error
	)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d == nil {
				return err
			}
			problems = append(problems, fmt.Errorf("reading %s: %w", path, err))
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			problems = append(problems, fmt.Errorf("stating %s: %w", path, err))
			return nil
		}
		found = append(found, TrashedFile{Path: path, ModTime: info.ModTime()})
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listing trash %s: %w", root, err)
	}
	return found, errors.Join(problems...)
}
