package provider

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eduardvoiculescu/agent-sessions/internal/session"
)

type fakeProvider struct {
	name    string
	history []session.Session
	live    []session.Session
	err     error
}

func (f fakeProvider) Name() string { return f.name }

func (f fakeProvider) Discover(context.Context, int) ([]session.Session, error) {
	return f.history, f.err
}

func (f fakeProvider) ResumeArgv(s session.Session, fork bool) ([]string, error) {
	if fork {
		return []string{f.name, "fork", s.ID}, nil
	}
	return []string{f.name, "resume", s.ID}, nil
}

type fakeLiveProvider struct {
	fakeProvider
	liveErr error
}

func (f fakeLiveProvider) Live(context.Context) ([]session.Session, error) {
	return f.live, f.liveErr
}

var (
	older = time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	newer = time.Date(2026, 8, 18, 11, 0, 0, 0, time.UTC)
)

func TestRegistrySessionsMergesAndSorts(t *testing.T) {
	claude := fakeLiveProvider{
		fakeProvider: fakeProvider{
			name:    "claude",
			history: []session.Session{{Agent: "claude", ID: "a", Name: "title", LastActive: older}},
			live:    []session.Session{{Agent: "claude", ID: "a", Live: true, PID: 7, Status: "running", LastActive: newer}},
		},
	}
	codex := fakeProvider{
		name:    "codex",
		history: []session.Session{{Agent: "codex", ID: "z", Name: "codex thread", LastActive: newer}},
	}

	got, err := NewRegistry(claude, codex).Sessions(context.Background(), 10)
	if err != nil {
		t.Fatalf("Sessions() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Sessions() returned %d sessions, want 2: %+v", len(got), got)
	}
	if !got[0].Live || got[0].ID != "a" {
		t.Errorf("Sessions()[0] = %+v, want the live claude session first", got[0])
	}
	if got[0].Name != "title" {
		t.Errorf("Sessions()[0].Name = %q, want the history title preserved", got[0].Name)
	}
	if got[1].ID != "z" {
		t.Errorf("Sessions()[1].ID = %q, want %q", got[1].ID, "z")
	}
}

func TestRegistrySessionsReportsPartialFailure(t *testing.T) {
	boom := errors.New("boom")
	broken := fakeProvider{name: "broken", err: boom}
	working := fakeProvider{
		name:    "working",
		history: []session.Session{{Agent: "working", ID: "ok", LastActive: newer}},
	}

	got, err := NewRegistry(broken, working).Sessions(context.Background(), 10)

	if !errors.Is(err, boom) {
		t.Errorf("Sessions() error = %v, want it to wrap %v", err, boom)
	}
	if !strings.Contains(err.Error(), "broken: boom") {
		t.Errorf("Sessions() error = %q, want it to name the failing provider", err)
	}
	if len(got) != 1 || got[0].ID != "ok" {
		t.Errorf("Sessions() = %+v, want the working provider's session despite the failure", got)
	}
}

func TestRegistryLiveSkipsProvidersWithoutLiveness(t *testing.T) {
	withLive := fakeLiveProvider{
		fakeProvider: fakeProvider{
			name: "claude",
			live: []session.Session{{Agent: "claude", ID: "a", Live: true, LastActive: newer}},
		},
	}
	withoutLive := fakeProvider{name: "codex"}

	got, err := NewRegistry(withLive, withoutLive).Live(context.Background())
	if err != nil {
		t.Fatalf("Live() error = %v", err)
	}
	if len(got) != 1 || got[0].Agent != "claude" {
		t.Errorf("Live() = %+v, want only the claude session", got)
	}
}

func TestRegistryFind(t *testing.T) {
	reg := NewRegistry(fakeProvider{name: "claude"})

	if _, ok := reg.Find("claude"); !ok {
		t.Error("Find(\"claude\") = not found, want found")
	}
	if _, ok := reg.Find("codex"); ok {
		t.Error("Find(\"codex\") = found, want not found")
	}
}

func TestRegistryStampsTheAgentOnEveryRow(t *testing.T) {
	forgetful := fakeLiveProvider{
		fakeProvider: fakeProvider{
			name:    "claude",
			history: []session.Session{{ID: "a", LastActive: older}},
			live:    []session.Session{{ID: "b", Live: true, LastActive: newer}},
		},
	}

	got, err := NewRegistry(forgetful).Sessions(context.Background(), 10)
	if err != nil {
		t.Fatalf("Sessions() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Sessions() returned %d sessions, want 2: %+v", len(got), got)
	}
	for _, s := range got {
		if s.Agent != "claude" {
			t.Errorf("Sessions() row %q has Agent %q, want the provider name stamped on it", s.ID, s.Agent)
		}
	}
}

type fakeStarterProvider struct {
	fakeProvider
}

func (f fakeStarterProvider) StartArgv() []string { return []string{f.name} }

func TestRegistryStartableListsOnlyStartersInRegistrationOrder(t *testing.T) {
	registry := NewRegistry(
		fakeProvider{name: "readonly"},
		fakeStarterProvider{fakeProvider{name: "claude"}},
		fakeStarterProvider{fakeProvider{name: "opencode"}},
	)

	got := registry.Startable()

	want := []string{"claude", "opencode"}
	if !slices.Equal(got, want) {
		t.Errorf("Startable() = %v, want %v", got, want)
	}
}

func TestRegistryStartableIsEmptyWithoutStarters(t *testing.T) {
	if got := NewRegistry(fakeProvider{name: "readonly"}).Startable(); len(got) != 0 {
		t.Errorf("Startable() = %v, want none", got)
	}
}
