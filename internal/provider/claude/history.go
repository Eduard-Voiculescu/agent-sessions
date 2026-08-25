package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	headWindow       = 64 << 10
	tailWindow       = 64 << 10
	wholeFileLimit   = 128 << 10
	forwardScanLimit = 1 << 20
	maxLineBytes     = 1 << 20
)

type transcript struct {
	path    string
	modTime time.Time
}

type meta struct {
	SessionID   string
	Title       string
	Cwd         string
	GitBranch   string
	FirstPrompt string
	IsSidechain bool
}

// parseState carries the saw-a-user-line flag alongside the result. The flag
// cannot be inferred from Cwd being empty: a session's first user line may
// legitimately carry no cwd, and treating that as "not found yet" lets a later
// line's cwd and branch overwrite it while FirstPrompt stays on the first,
// stitching one meta out of two different messages.
type parseState struct {
	meta
	sawUser bool
}

type transcriptLine struct {
	Type        string `json:"type"`
	SessionID   string `json:"sessionId"`
	AITitle     string `json:"aiTitle"`
	Cwd         string `json:"cwd"`
	GitBranch   string `json:"gitBranch"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func discover(root string) ([]transcript, error) {
	var found []transcript

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d == nil {
				return err
			}
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			// Subagent transcripts are pruned here, before --limit is applied, so
			// the parse budget is not spent on files that are then discarded.
			// parseTranscript's isSidechain check remains the correctness gate.
			if d.Name() == "subagents" {
				return fs.SkipDir
			}
			return nil
		}
		// A DirEntry's type comes from lstat, so a symlink is identifiable here
		// before anything opens it.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if filepath.Ext(path) != ".jsonl" {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		found = append(found, transcript{path: path, modTime: info.ModTime()})
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("walking claude transcripts %s: %w", root, err)
	}

	return found, nil
}

// openTranscript refuses a symbolic link. A .jsonl under the projects directory
// is opened and its content drawn in the preview, so a link planted there would
// turn the picker into a reader for any file its owner can open. O_NOFOLLOW
// decides it in the open itself rather than in a check the file could be swapped
// out from under.
func openTranscript(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, syscall.ELOOP) {
		return nil, fmt.Errorf("%s is a symbolic link, not a transcript", path)
	}
	if err != nil {
		return nil, fmt.Errorf("opening transcript %s: %w", path, err)
	}
	return file, nil
}

func parseTranscript(path string) (meta, error) {
	file, err := openTranscript(path)
	if err != nil {
		return meta{}, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return meta{}, fmt.Errorf("stating transcript %s: %w", path, err)
	}

	var parsed parseState
	onLine := func(line []byte) { apply(line, &parsed) }

	if info.Size() <= wholeFileLimit {
		absorbWhole(file, wholeFileLimit, onLine)
		return parsed.meta, nil
	}

	absorb(io.NewSectionReader(file, 0, headWindow), headWindow, onLine)

	// SessionStart hook output lands ahead of the first user line and can be
	// hundreds of kilobytes, so the head window may hold no user line at all.
	if !parsed.sawUser {
		absorb(io.NewSectionReader(file, 0, forwardScanLimit), forwardScanLimit, onLine)
	}

	tailAt := info.Size() - tailWindow
	absorbTail(io.NewSectionReader(file, tailAt, tailWindow), tailWindow, onLine)

	return parsed.meta, nil
}

// foldMode says how the two ends of a read must be treated. Both flags are false
// for a window cut out of the middle of a file, which begins and ends mid-line.
type foldMode struct {
	skipLeading bool
	keepFinal   bool
}

// absorb folds every complete line of r into handle, skipping the trailing
// partial line that a windowed read always ends on.
func absorb(r io.Reader, limit int64, handle func([]byte)) {
	fold(bufio.NewReaderSize(io.LimitReader(r, limit), 64<<10), foldMode{}, handle)
}

// absorbWhole folds a read that runs to the end of the file, where a final line
// with no newline after it is a whole record and not a fragment.
func absorbWhole(r io.Reader, limit int64, handle func([]byte)) {
	fold(bufio.NewReaderSize(io.LimitReader(r, limit), 64<<10), foldMode{keepFinal: true}, handle)
}

// absorbTail drops the leading partial line before folding, because a tail read
// starts mid-line. It ends at the end of the file, so its last line is whole.
func absorbTail(r io.Reader, limit int64, handle func([]byte)) {
	fold(bufio.NewReaderSize(io.LimitReader(r, limit), 64<<10), foldMode{skipLeading: true, keepFinal: true}, handle)
}

// fold reads complete lines out of reader per mode, handing each one to
// handle. Both absorbTail (meta parsing) and Preview (message extraction)
// share this so the 464 KB-line and mid-line-seam handling readLine gives them
// cannot drift apart between the two.
func fold(reader *bufio.Reader, mode foldMode, handle func([]byte)) {
	if mode.skipLeading {
		if _, err := readLine(reader); err != nil {
			return
		}
	}
	for {
		line, err := readLine(reader)
		switch {
		case err == nil:
			if len(line) > 0 {
				handle(line)
			}
		case mode.keepFinal && errors.Is(err, io.EOF) && len(line) > 0:
			handle(line)
			return
		default:
			return
		}
	}
}

// readLine returns one complete line. A line longer than maxLineBytes is
// discarded rather than buffered; real transcripts reach 464 KB, so the caller
// must never assume a 64 KB ceiling.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(line)+len(chunk) <= maxLineBytes {
			line = append(line, chunk...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimRight(line, "\r\n"), err
	}
}

func apply(raw []byte, dst *parseState) {
	var line transcriptLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return
	}

	if dst.SessionID == "" {
		dst.SessionID = line.SessionID
	}

	switch line.Type {
	case "ai-title":
		if line.AITitle != "" {
			dst.Title = line.AITitle
		}
	case "user":
		if !dst.sawUser {
			dst.sawUser = true
			dst.Cwd = line.Cwd
			dst.GitBranch = line.GitBranch
			dst.FirstPrompt = promptText(line.Message.Content)
			dst.IsSidechain = line.IsSidechain
		}
	}
}

// promptText reads the prompt out of a message whose content is either a plain
// string or an array of typed blocks.
func promptText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}

	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		return text
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return ""
	}
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			return block.Text
		}
	}
	return ""
}
