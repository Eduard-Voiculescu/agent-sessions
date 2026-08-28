package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

type watchOptions struct {
	interval time.Duration
	once     bool
}

// watchFrame is one line of the feed. Sessions is the same type list --json
// emits, so the two commands cannot disagree about a session's shape, and Worst
// and Attention are computed here so no consumer has to rank statuses.
type watchFrame struct {
	At        time.Time         `json:"at"`
	Worst     string            `json:"worst"`
	Attention int               `json:"attention"`
	Sessions  []session.Session `json:"sessions"`
}

func newWatchCommand(opts *Options) *cobra.Command {
	var watchOpts watchOptions

	cmd := &cobra.Command{
		Use:           "watch",
		Short:         "Emit one JSON line per tick describing every session",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWatch(cmd, opts, watchOpts)
		},
	}
	cmd.Flags().DurationVar(&watchOpts.interval, "interval", 2*time.Second, "how often to emit a frame")
	cmd.Flags().BoolVar(&watchOpts.once, "once", false, "emit a single frame and exit")

	return cmd
}

// runWatch emits frames until its context is cancelled. A provider problem is a
// warning on stderr rather than an exit: what reads this feed is a creature in
// the corner of a screen, and one unreadable transcript must not take it down.
// Nothing but a frame goes to stdout, so a decode failure downstream means a
// real protocol break rather than a stray log line.
func runWatch(cmd *cobra.Command, opts *Options, watchOpts watchOptions) error {
	if watchOpts.interval <= 0 {
		return fmt.Errorf("--interval must be positive, got %s", watchOpts.interval)
	}

	ctx := cmd.Context()
	encoder := json.NewEncoder(cmd.OutOrStdout())
	ticker := time.NewTicker(watchOpts.interval)
	defer ticker.Stop()

	for {
		sessions, err := loadSessions(ctx, opts)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
		}

		attention := session.Classify(sessions)
		frame := watchFrame{
			At:        time.Now().UTC(),
			Worst:     attention.Worst,
			Attention: attention.Count,
			Sessions:  sessions,
		}
		if err := encoder.Encode(frame); err != nil {
			return fmt.Errorf("encoding the feed: %w", err)
		}

		if watchOpts.once {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
