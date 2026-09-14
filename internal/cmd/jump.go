package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

type jumpOptions struct {
	pid     int
	session string
	picker  bool
}

// newJumpCommand exposes the picker's ctrl+j as a command, so something outside
// a terminal — the attention pet — can put a session's pane in front without
// learning AppleScript. jump and picker are parameters so the suite drives them
// without osascript or a process listing, the way pickerConfig's tests already do.
func newJumpCommand(opts *Options, jump func(context.Context, int) error, picker func(context.Context) (int, error)) *cobra.Command {
	var jumpOpts jumpOptions

	cmd := &cobra.Command{
		Use:           "jump",
		Short:         "Focus the terminal pane running a session",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runJump(cmd, opts, jumpOpts, jump, picker)
		},
	}
	cmd.Flags().IntVar(&jumpOpts.pid, "pid", 0, "pid of the process to focus")
	cmd.Flags().StringVar(&jumpOpts.session, "session", "", "session id to focus, resolved to its pid")
	cmd.Flags().BoolVar(&jumpOpts.picker, "picker", false, "focus the terminal pane running the picker itself")

	return cmd
}

func runJump(cmd *cobra.Command, opts *Options, jumpOpts jumpOptions, jump func(context.Context, int) error, picker func(context.Context) (int, error)) error {
	aimed := 0
	for _, given := range []bool{jumpOpts.pid != 0, jumpOpts.session != "", jumpOpts.picker} {
		if given {
			aimed++
		}
	}
	switch {
	case aimed > 1:
		return fmt.Errorf("pass one of --pid, --session or --picker, not several")
	case aimed == 0:
		return fmt.Errorf("pass --pid <n>, --session <id> or --picker")
	}

	ctx := cmd.Context()
	pid := jumpOpts.pid
	if jumpOpts.picker {
		found, err := picker(ctx)
		if err != nil {
			return err
		}
		pid = found
	}
	if jumpOpts.session != "" {
		resolved, err := livePID(ctx, opts, jumpOpts.session)
		if err != nil {
			return err
		}
		pid = resolved
	}

	if pid <= 0 {
		return fmt.Errorf("--pid must be positive, got %d", pid)
	}
	return jump(ctx, pid)
}

// livePID finds the running process for a session id. Only the live registry is
// consulted: a session with no process has no pane to focus, and saying so is
// more use than jumping somewhere arbitrary.
func livePID(ctx context.Context, opts *Options, id string) (int, error) {
	live, err := registryFor(opts).Live(ctx)
	// A non-nil error may accompany a usable slice — one provider failing must
	// not hide another's answer — so the search runs before the error is judged.
	for _, s := range live {
		if s.ID == id && s.PID > 0 {
			return s.PID, nil
		}
	}
	if err != nil {
		return 0, fmt.Errorf("no live session with id %q: %w", id, err)
	}
	return 0, fmt.Errorf("no live session with id %q", id)
}
