package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points every location this package resolves from the environment at
// an empty temporary directory, so no test reads the developer's own state.
// Both halves are load-bearing and both were found by a test that failed:
// config.Load in the command's PersistentPreRunE made flag parsing depend on
// whatever the machine had configured, and a provider that defaults to a path
// under $HOME put real sessions — someone's real directory names — into the
// output of a test that had built its own fixture.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "agent-sessions-cmd-tests")
	if err != nil {
		panic(err)
	}
	os.Setenv("AGENT_SESSIONS_CONFIG", filepath.Join(dir, "absent"))
	os.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	os.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))

	// os.Exit skips deferred calls, so the removal runs before it.
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
