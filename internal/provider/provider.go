// Package provider adapts each coding agent's on-disk state to sessions.
package provider

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

// Provider turns one coding agent's stored state into sessions and knows how to
// re-enter them.
type Provider interface {
	Name() string
	Discover(ctx context.Context, limit int) ([]session.Session, error)
	ResumeArgv(s session.Session, fork bool) ([]string, error)
}

// Prompter is implemented only by agents that accept an opening message when
// resuming. A session with no live process cannot be typed into, so sending it
// a message means resuming it with that message as the first thing said.
type Prompter interface {
	ResumeWithPromptArgv(s session.Session, prompt string) ([]string, error)
}

// Liveness is implemented only by agents that publish a registry of running
// processes. Agents that cannot report it are listed with no status.
type Liveness interface {
	Live(ctx context.Context) ([]session.Session, error)
}

type Registry struct {
	providers []Provider
}

func NewRegistry(providers ...Provider) *Registry {
	return &Registry{providers: providers}
}

// Sessions returns every provider's sessions, live state merged over history and
// sorted for display. A non-nil error may accompany a non-empty slice: one
// provider failing must not hide the rest.
func (r *Registry) Sessions(ctx context.Context, limit int) ([]session.Session, error) {
	history, historyErr := r.collect(ctx, func(p Provider) ([]session.Session, error) {
		return p.Discover(ctx, limit)
	})
	live, liveErr := r.Live(ctx)

	merged := session.Merge(history, live)
	session.Sort(merged)

	return merged, errors.Join(historyErr, liveErr)
}

func (r *Registry) Live(ctx context.Context) ([]session.Session, error) {
	return r.collect(ctx, func(p Provider) ([]session.Session, error) {
		reporter, ok := p.(Liveness)
		if !ok {
			return nil, nil
		}
		return reporter.Live(ctx)
	})
}

// Providers returns the registered providers, in registration order.
func (r *Registry) Providers() []Provider {
	return slices.Clone(r.providers)
}

func (r *Registry) Find(agent string) (Provider, bool) {
	for _, p := range r.providers {
		if p.Name() == agent {
			return p, true
		}
	}
	return nil, false
}

func (r *Registry) collect(ctx context.Context, fetch func(Provider) ([]session.Session, error)) ([]session.Session, error) {
	var (
		mu       sync.Mutex
		sessions []session.Session
		failures []error
	)

	var group errgroup.Group
	for _, p := range r.providers {
		group.Go(func() error {
			found, err := fetch(p)

			// Agent is stamped here rather than trusted from each provider:
			// resume dispatch looks the provider up by this field once the
			// picker has already exited, where a mismatch has nowhere to go.
			for i := range found {
				found[i].Agent = p.Name()
			}

			mu.Lock()
			defer mu.Unlock()
			sessions = append(sessions, found...)
			if err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", p.Name(), err))
			}
			return nil
		})
	}
	// Every worker returns nil; failures travel in the slice instead, so one
	// provider's error cannot cancel the others.
	group.Wait()

	return sessions, errors.Join(failures...)
}
