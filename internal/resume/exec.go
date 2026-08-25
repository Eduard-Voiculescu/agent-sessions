// Package resume re-enters a session by replacing the current process.
package resume

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

type Runner struct {
	lookPath func(string) (string, error)
	chdir    func(string) error
	exec     func(argv0 string, argv, env []string) error
}

type Option func(*Runner)

func WithLookPath(fn func(string) (string, error)) Option {
	return func(r *Runner) { r.lookPath = fn }
}

func WithChdir(fn func(string) error) Option {
	return func(r *Runner) { r.chdir = fn }
}

func WithExec(fn func(argv0 string, argv, env []string) error) Option {
	return func(r *Runner) { r.exec = fn }
}

// New defaults to real syscalls; every one is injectable for tests.
func New(opts ...Option) *Runner {
	r := &Runner{
		lookPath: exec.LookPath,
		chdir:    os.Chdir,
		exec:     syscall.Exec,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Run enters the session's directory and replaces this process with argv. On
// success it does not return.
func (r *Runner) Run(s session.Session, argv []string) error {
	if len(argv) == 0 {
		return errors.New("no command to run")
	}

	binary, err := r.lookPath(argv[0])
	if err != nil {
		return fmt.Errorf("cannot run %q: %w", strings.Join(argv, " "), err)
	}

	if s.Cwd != "" {
		if err := r.chdir(s.Cwd); err != nil {
			return fmt.Errorf("entering %s: %w", s.Cwd, err)
		}
	}

	if err := r.exec(binary, argv, os.Environ()); err != nil {
		return fmt.Errorf("running %q: %w", strings.Join(argv, " "), err)
	}
	return nil
}
