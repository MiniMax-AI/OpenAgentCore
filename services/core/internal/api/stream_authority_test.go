package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

type streamAuthorityResolver struct {
	keys        fixtureKeyResolver
	unavailable atomic.Bool
	calls       atomic.Int32
}

func (r *streamAuthorityResolver) ResolveProjectAPIKey(ctx context.Context, digest string) (store.ProjectAPIKeyBinding, error) {
	r.calls.Add(1)
	if r.unavailable.Load() {
		return store.ProjectAPIKeyBinding{}, errors.New("resolver unavailable")
	}
	return r.keys.ResolveProjectAPIKey(ctx, digest)
}

type busyAuthorityStream struct {
	*streamFixture
	sequence int64
}

func (s *busyAuthorityStream) ListSessionEvents(ctx context.Context, _, _ string, _ int64) ([]sessions.SessionChange, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	s.sequence++
	return []sessions.SessionChange{{Sequence: s.sequence + 10, Event: v1.SessionEvent{Type: "agent.session.idle", EventID: "busy"}}}, nil
}

func TestBusyStreamRechecksAuthorityAndFailsClosed(t *testing.T) {
	key := callerBinding()
	key.TokenSHA256 = runtimedevice.HashCredential("stream")
	resolver := &streamAuthorityResolver{keys: projectKeys(t, key)}
	f := &busyAuthorityStream{streamFixture: &streamFixture{session: store.Session{ID: uuid.NewString(), TenantID: key.TenantID, CreatedAt: time.Now(), Metadata: map[string]string{}, Configuration: json.RawMessage(`{"agent":{"id":"agent_fixture","model":"fixture","tools":[]},"environment":{"type":"none"}}`)}}}
	deps, fakes := testDependencies(t)
	fakes.projects.resolveProjectAPIKey = resolver.ResolveProjectAPIKey
	f.serve(fakes)
	fakes.sessionEvents.listSessionEvents = f.ListSessionEvents
	h := newTestHandler(t, deps)
	server := httptest.NewServer(h)
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/agents/sessions/"+f.session.ID+"/events", nil)
	r.Header.Set("Authorization", "Bearer stream")
	r.Header.Set("OpenAI-Beta", "agents=v1")
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("stream status", response.StatusCode)
	}
	done := make(chan error, 1)
	// Fail the resolver only after receiving real event data.
	go func() {
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			if scanner.Text() == "event: agent.session.idle" {
				resolver.unavailable.Store(true)
			}
		}
		done <- scanner.Err()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("busy stream bypassed authority rechecks")
	}
	if n := resolver.calls.Load(); n != 3 {
		t.Fatal("unexpected per-event authority queries", n)
	}
}
