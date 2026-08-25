package cmd

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/eduardvoiculescu/agent-sessions/internal/action"
	"github.com/eduardvoiculescu/agent-sessions/internal/provider/claude"
)

type purgeOptions struct {
	olderThan time.Duration
	dryRun    bool
}

func newPurgeCommand(opts *Options) *cobra.Command {
	var purgeOpts purgeOptions

	cmd := &cobra.Command{
		Use:           "purge",
		Short:         "Permanently delete transcripts previously moved to trash",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, _ []string) error {
			return runPurge(c.OutOrStdout(), opts, purgeOpts)
		},
	}
	cmd.Flags().DurationVar(&purgeOpts.olderThan, "older-than", 0, "delete transcripts that have been in the trash longer than this (e.g. 720h)")
	cmd.Flags().BoolVar(&purgeOpts.dryRun, "dry-run", false, "report what would be deleted without deleting")

	return cmd
}

func runPurge(out io.Writer, opts *Options, purgeOpts purgeOptions) error {
	dir := cmp.Or(opts.ClaudeDir, claude.Dir())

	if purgeOpts.olderThan < 0 {
		return fmt.Errorf("--older-than must not be negative, got %s", purgeOpts.olderThan)
	}

	// TrashedFiles returns what it could read alongside its per-entry problems,
	// so an unreadable project directory must not abandon the whole run — only a
	// failure that yielded nothing is fatal.
	trashed, err := action.TrashedFiles(dir)
	if err != nil {
		if len(trashed) == 0 {
			return err
		}
		fmt.Fprintf(out, "warning: %v\n", err)
	}
	if len(trashed) == 0 {
		fmt.Fprintf(out, "trash is empty (%s)\n", action.TrashRoot(dir))
		return nil
	}

	cutoff := time.Now().Add(-purgeOpts.olderThan)
	// One signal decides whether anything is deleted at all. An earlier version
	// used `== 0` here and `> 0` on the per-file age guard; a negative duration
	// satisfied neither and wiped the whole trash.
	report := purgeOpts.dryRun || purgeOpts.olderThan <= 0

	var deleted, failed int
	for _, f := range trashed {
		if f.ModTime.After(cutoff) {
			continue
		}
		if report {
			// The picker's footer has no room for the restore command, so this
			// listing is the one place a user can read where a deleted
			// transcript went and how to put it back.
			fmt.Fprintf(out, "would delete %s\n  restore with: %s\n", f.Path, action.RestoreCommand(dir, f.Path))
			continue
		}
		if err := os.Remove(f.Path); err != nil {
			fmt.Fprintf(out, "could not delete %s: %v\n", f.Path, err)
			failed++
			continue
		}
		deleted++
	}

	if report {
		if purgeOpts.dryRun {
			fmt.Fprintf(out, "dry run: nothing deleted\n")
		} else {
			fmt.Fprintf(out, "nothing deleted; pass --older-than to purge\n")
		}
		return nil
	}
	fmt.Fprintf(out, "deleted %d transcript(s), %d failed\n", deleted, failed)
	if failed > 0 {
		return fmt.Errorf("%d transcript(s) could not be deleted", failed)
	}
	return nil
}
