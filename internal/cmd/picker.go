package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// binaryName is what the picker's own process is called. A row naming anything
// else is somebody else's program.
const binaryName = "agent-sessions"

// psTimeout bounds the process listing. Nothing here waits on a human, unlike
// the AppleScript this feeds, so a second is generous.
const psTimeout = time.Second

// PickerPID is the pid of the running picker, for something outside a terminal —
// the attention pet — to aim a jump at.
//
// The picker does not announce itself anywhere, so it is recognised rather than
// looked up: the process list already says everything needed, and a pidfile
// would be state to write, to leave stale, and to explain in SECURITY.md.
func PickerPID(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, psTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,etime=,tty=,args=").Output()
	if err != nil {
		return 0, fmt.Errorf("listing processes: %w", err)
	}
	return findPicker(string(out), subcommandNames(), os.Getpid())
}

// subcommandNames is every verb this binary answers to, read off the command
// tree rather than listed again here: a new subcommand must not quietly start
// looking like the picker.
func subcommandNames() []string {
	root := newRootCommand(&Options{}, runners{})

	names := make([]string, 0, len(root.Commands()))
	for _, command := range root.Commands() {
		names = append(names, command.Name())
	}
	return names
}

// findPicker is the recognising half, kept apart from the process listing so the
// shapes ps reports can be tested without one.
//
// The picker is the agent-sessions process that names no subcommand and holds a
// terminal. Both halves matter: the pet's own `watch` child is an agent-sessions
// process too, and a process with no tty has no pane to put in front.
func findPicker(psOutput string, subcommands []string, self int) (int, error) {
	best, bestAge := 0, time.Duration(0)

	for _, line := range strings.Split(psOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid == self {
			continue
		}
		age, err := parseElapsed(fields[1])
		if err != nil {
			continue
		}
		if tty := fields[2]; tty == "??" || tty == "-" {
			continue
		}
		if !isPicker(fields[3:], subcommands) {
			continue
		}

		if best == 0 || age < bestAge {
			best, bestAge = pid, age
		}
	}

	if best == 0 {
		return 0, errors.New("no agent-sessions picker is running")
	}
	return best, nil
}

// isPicker reads one process's argv. A subcommand anywhere in it disqualifies
// the row rather than only in first position: `agent-sessions --live list` is
// not a picker either, and the flags that could carry a subcommand's name as a
// value take agent names and paths, never a verb.
func isPicker(argv []string, subcommands []string) bool {
	if filepath.Base(argv[0]) != binaryName {
		return false
	}
	return !slices.ContainsFunc(argv[1:], func(arg string) bool {
		return slices.Contains(subcommands, arg)
	})
}

// parseElapsed reads ps's own elapsed-time spelling, which is [[dd-]hh:]mm:ss.
// It is what orders two pickers: the newest is the one just reached for, and the
// older is the one already left behind.
func parseElapsed(value string) (time.Duration, error) {
	days := 0
	if dash := strings.IndexByte(value, '-'); dash >= 0 {
		parsed, err := strconv.Atoi(value[:dash])
		if err != nil {
			return 0, fmt.Errorf("reading elapsed time %q: %w", value, err)
		}
		days, value = parsed, value[dash+1:]
	}

	parts := strings.Split(value, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("reading elapsed time %q", value)
	}

	elapsed := time.Duration(days) * 24 * time.Hour
	for _, unit := range []time.Duration{time.Hour, time.Minute, time.Second}[3-len(parts):] {
		count, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, fmt.Errorf("reading elapsed time %q: %w", value, err)
		}
		elapsed += time.Duration(count) * unit
		parts = parts[1:]
	}
	return elapsed, nil
}
