// Package config reads the file that decides which actions the picker offers
// and what they act through.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// File is a parsed config. An empty field means the key was absent, which is
// what lets the caller tell "the owner chose this" from "the owner said
// nothing" — the two resolve differently, one to a choice and one to detection.
type File struct {
	// Path is where the file was read from, and "" when none exists.
	Path    string
	Editor  string
	VCS     string
	Tickets Tickets
	Forge   string
	Hidden  []string
	// Limit and Live are pointers so that a key set to its own zero value —
	// limit = 0, live = false — is distinguishable from a key nobody wrote.
	Limit     *int
	Live      *bool
	Cwd       string
	ClaudeDir string
	// Providers is an allowlist when non-empty and every compiled provider when
	// empty, so a fresh install with no config lists every agent it can read.
	Providers   []string
	OpencodeDir string
	Overlay     Overlay
}

// Overlay is the attention pet's own settings. Pointers for the numbers, like
// Limit and Live, so a key set to its own zero value is distinguishable from a
// key nobody wrote — an offset of 0,0 is a corner someone chose.
type Overlay struct {
	Corner  string
	OffsetX *int
	OffsetY *int
	Size    *int
	Raise   []string
	Sound   *bool
}

type Tickets struct {
	Provider  string
	Workspace string
	Prefixes  []string
}

// schema is every section and key this file understands, and the whole of the
// validation rule. Anything absent from here is a typo, and saying so is the
// point: a setting silently ignored reads as a setting applied.
var schema = map[string][]string{
	"actions":   {"hide"},
	"claude":    {"dir"},
	"editor":    {"default"},
	"filters":   {"live", "cwd"},
	"forge":     {"provider"},
	"list":      {"limit"},
	"opencode":  {"dir"},
	"overlay":   {"corner", "offset", "raise", "size", "sound"},
	"providers": {"enable"},
	"tickets":   {"provider", "workspace", "prefixes"},
	"vcs":       {"default"},
}

// Path is the file Load reads, whether or not it exists. It is "" only when no
// home directory can be determined and nothing overrode the location.
func Path() string {
	if explicit := os.Getenv("AGENT_SESSIONS_CONFIG"); explicit != "" {
		return explicit
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "agent-sessions", "config")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".agent-sessions", "config")
}

func Load() (File, error) {
	path := Path()
	if path == "" {
		return File{}, nil
	}
	return LoadFrom(path)
}

// LoadFrom reads one named path. A missing file is not an error: a machine that
// has never configured anything is the common case, and every setting falls
// through to detection or a built-in default.
func LoadFrom(path string) (File, error) {
	handle, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return File{}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("reading %s: %w", path, err)
	}
	defer handle.Close()

	info, err := handle.Stat()
	if err != nil {
		return File{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if info.IsDir() {
		return File{}, fmt.Errorf("%s is a directory, not a config file", path)
	}

	values, err := parse(handle, path)
	if err != nil {
		return File{}, err
	}

	file := File{
		Path:   path,
		Editor: values.text("editor", "default"),
		VCS:    values.text("vcs", "default"),
		Forge:  values.text("forge", "provider"),
		Hidden: list(values.text("actions", "hide")),
		Tickets: Tickets{
			Provider:  values.text("tickets", "provider"),
			Workspace: values.text("tickets", "workspace"),
			Prefixes:  list(values.text("tickets", "prefixes")),
		},
		// A path in a config file is read by no shell, so a leading ~ would
		// otherwise reach the filter as a literal character and match nothing at
		// all — silently, which is the worst way for a filter to be wrong.
		Cwd:         expandHome(values.text("filters", "cwd")),
		ClaudeDir:   expandHome(values.text("claude", "dir")),
		Providers:   list(values.text("providers", "enable")),
		OpencodeDir: expandHome(values.text("opencode", "dir")),
	}

	if file.Limit, err = values.integer(path, "list", "limit"); err != nil {
		return File{}, err
	}
	if file.Live, err = values.boolean(path, "filters", "live"); err != nil {
		return File{}, err
	}

	file.Overlay.Corner = values.text("overlay", "corner")
	file.Overlay.Raise = list(values.text("overlay", "raise"))
	if file.Overlay.Size, err = values.integer(path, "overlay", "size"); err != nil {
		return File{}, err
	}
	if file.Overlay.Sound, err = values.boolean(path, "overlay", "sound"); err != nil {
		return File{}, err
	}
	if file.Overlay.OffsetX, file.Overlay.OffsetY, err = values.point(path, "overlay", "offset"); err != nil {
		return File{}, err
	}
	return file, nil
}

// setting is one parsed key, with the line it came from so a value that is the
// wrong type can be reported where it was written.
type setting struct {
	text string
	line int
}

type settings map[string]map[string]setting

func (v settings) text(section, key string) string { return v[section][key].text }

func (v settings) integer(path, section, key string) (*int, error) {
	raw, ok := v[section][key]
	if !ok || raw.text == "" {
		return nil, nil
	}

	n, err := strconv.Atoi(raw.text)
	if err != nil {
		return nil, fmt.Errorf("%s:%d: %s in [%s] must be a whole number, not %q", path, raw.line, key, section, raw.text)
	}
	if n < 0 {
		return nil, fmt.Errorf("%s:%d: %s in [%s] cannot be negative (0 means no limit)", path, raw.line, key, section)
	}
	return &n, nil
}

// boolean takes true and false only. yes, on and 1 are not accepted: guessing
// at synonyms is how a config file ends up with two spellings for one setting
// and no way to tell which one a reader meant.
func (v settings) boolean(path, section, key string) (*bool, error) {
	raw, ok := v[section][key]
	if !ok || raw.text == "" {
		return nil, nil
	}

	switch raw.text {
	case "true":
		yes := true
		return &yes, nil
	case "false":
		no := false
		return &no, nil
	}
	return nil, fmt.Errorf("%s:%d: %s in [%s] must be true or false, not %q", path, raw.line, key, section, raw.text)
}

// point takes "x,y" of two non-negative whole numbers. An offset is measured
// from a corner, so a negative one would place the pet off the screen it was
// asked to sit on.
func (v settings) point(path, section, key string) (*int, *int, error) {
	raw, ok := v[section][key]
	if !ok || raw.text == "" {
		return nil, nil, nil
	}

	parts := strings.Split(raw.text, ",")
	if len(parts) != 2 {
		return nil, nil, fmt.Errorf("%s:%d: %s in [%s] must be two whole numbers as x,y, not %q", path, raw.line, key, section, raw.text)
	}

	pair := make([]int, 2)
	for i, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 0 {
			return nil, nil, fmt.Errorf("%s:%d: %s in [%s] must be two non-negative whole numbers as x,y, not %q", path, raw.line, key, section, raw.text)
		}
		pair[i] = n
	}
	return &pair[0], &pair[1], nil
}

// expandHome resolves a leading ~ against the home directory. Only a leading
// one, and only followed by a separator or nothing: ~other is another user's
// home on some systems and this does not pretend to resolve that.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

func parse(handle *os.File, path string) (settings, error) {
	values := make(settings, len(schema))
	for name := range schema {
		values[name] = map[string]setting{}
	}

	section := ""
	scanner := bufio.NewScanner(handle)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || isComment(text) {
			continue
		}

		if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
			section = strings.TrimSpace(text[1 : len(text)-1])
			if _, ok := schema[section]; !ok {
				return nil, fmt.Errorf("%s:%d: unknown section [%s]: known sections are %s", path, line, section, sections())
			}
			continue
		}

		key, value, found := strings.Cut(text, "=")
		if !found {
			return nil, fmt.Errorf("%s:%d: expected \"key = value\"", path, line)
		}
		key = strings.TrimSpace(key)

		if section == "" {
			return nil, fmt.Errorf("%s:%d: key %q outside any section", path, line, key)
		}
		if !slices.Contains(schema[section], key) {
			return nil, fmt.Errorf("%s:%d: unknown key %q in [%s]: known keys are %s", path, line, key, section, strings.Join(schema[section], ", "))
		}
		if _, seen := values[section][key]; seen {
			return nil, fmt.Errorf("%s:%d: duplicate key %q in [%s]", path, line, key, section)
		}
		values[section][key] = setting{text: clean(value), line: line}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return values, nil
}

func isComment(text string) bool {
	return strings.HasPrefix(text, "#") || strings.HasPrefix(text, ";")
}

// clean trims a value and drops any trailing comment. Values are neither quoted
// nor escaped, so there is nothing to protect a # inside one — and nothing
// configured here needs one. A value that did would be the signal to reconsider
// the format rather than to grow an escaping scheme.
func clean(value string) string {
	value = strings.TrimSpace(value)
	if isComment(value) {
		return ""
	}
	for _, marker := range []string{" #", " ;", "\t#", "\t;"} {
		if i := strings.Index(value, marker); i >= 0 {
			value = value[:i]
		}
	}
	return strings.TrimSpace(value)
}

func list(value string) []string {
	var out []string
	for _, element := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(element); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// sections is sorted rather than in map order, so the same mistake reports the
// same message on every run.
func sections() string {
	names := make([]string, 0, len(schema))
	for name := range schema {
		names = append(names, name)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}
