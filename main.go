package main

import (
	"fmt"
	"os"

	"github.com/eduardvoiculescu/agent-sessions/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "agent-sessions:", err)
		os.Exit(1)
	}
}
