package termjump_test

import (
	"context"
	"os"
	"testing"

	"github.com/eduardvoiculescu/agent-sessions/internal/termjump"
)

// TestLiveStartAgainstRealITerm is the one thing the injected run func cannot
// check: whether iTerm2 accepts the script at all. An AppleScript syntax error
// arrives as "exit status 1", so a suite that never runs osascript would pass on
// a script no iTerm2 can parse. Opt-in, because it opens a tab in whatever
// window is in front.
//
//	LIVE_ITERM=1 go test ./internal/termjump/ -run TestLiveStart
func TestLiveStartAgainstRealITerm(t *testing.T) {
	if os.Getenv("LIVE_ITERM") == "" {
		t.Skip("set LIVE_ITERM=1 to drive the real iTerm2")
	}
	if err := termjump.New().Start(context.Background(), os.Getenv("HOME"), []string{"echo", "agent-sessions ^n works"}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
}
