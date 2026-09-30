package node

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

type observationProvider struct {
	*fakeProvider
	observationMu sync.Mutex
	targets       []runtimeobs.Target
	sample        runtimeobs.Sample
	err           error
}

func (p *observationProvider) Observe(_ context.Context, target runtimeobs.Target) (runtimeobs.Sample, error) {
	p.observationMu.Lock()
	defer p.observationMu.Unlock()
	p.targets = append(p.targets, target)
	if target.Instance.ComputePhase == "suspended" {
		return runtimeobs.Sample{}, runtimeobs.ErrNotRunning
	}
	return p.sample, p.err
}
func observationTarget(r sandbox.Reference, installation string) runtimeobs.Target {
	return runtimeobs.Target{TenantID: r.TenantID, SessionID: uuid.NewString(), EnvironmentID: r.EnvironmentID, Mode: runtimeobs.ModeManaged,
		Instance:   runtimeobs.Instance{AllocationID: r.AllocationID, ProviderKey: installation, AllocationState: "running", ComputePhase: "running", ProviderState: json.RawMessage(`{"current":{"id":"exact-incarnation","generation":3}}`)},
		TokenUsage: &runtimeobs.TokenUsage{InputTokens: 123, OutputTokens: 456}}
}
func runObservationNode(t *testing.T, hub *Hub, url string, id Identity, provider sandbox.SandboxProvider) context.CancelFunc {
	t.Helper()
	dir := stateDir(t)
	stored, err := InitIdentity(dir, url, id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, AgentConfig{CoreURL: url, StateDirectory: dir, Identity: id, Credential: stored.Credential, Provider: provider, Probe: func(context.Context) (Health, error) { return Health{}, errors.New("provider not ready for creation") }})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("observation node did not stop")
		}
	})
	wait(t, func() bool { return hub.Online(id.NodeID) })
	return cancel
}
func TestObservationsRouteThroughAssignedNodeWithoutLifecycleCalls(t *testing.T) {
	first, second := identity(), identity()
	second.InstallationID = first.InstallationID
	hub := NewHub(HubOptions{Authenticate: func(_ context.Context, id, _ string) (Identity, error) {
		switch id {
		case first.NodeID:
			return first, nil
		case second.NodeID:
			return second, nil
		}
		return Identity{}, ErrAuthentication
	}, OwnerEpoch: func(context.Context) (uint64, error) { return 1, nil }})
	server := httptest.NewServer(hub)
	t.Cleanup(server.Close)
	t.Cleanup(hub.Close)
	zero, bytes := uint64(0), uint64(1234)
	now := time.Now().UTC().Truncate(time.Microsecond)
	a := &observationProvider{fakeProvider: &fakeProvider{}, sample: runtimeobs.Sample{ObservedAt: now, MemoryUsageBytes: &zero}}
	b := &observationProvider{fakeProvider: &fakeProvider{}, sample: runtimeobs.Sample{ObservedAt: now, MemoryUsageBytes: &bytes}}
	runObservationNode(t, hub, server.URL, first, a)
	stopSecond := runObservationNode(t, hub, server.URL, second, b)
	ra, rb := reference(), reference()
	assignments := map[sandbox.Reference]string{ra: first.NodeID, rb: second.NodeID}
	source := hub.GenerationProvider("docker", func(_ context.Context, r sandbox.Reference) (string, uint64, error) {
		if id, ok := assignments[r]; ok {
			return id, 1, nil
		}
		return "", 0, sandbox.ErrOwnership
	}).(runtimeobs.Source)
	if typed := source.ObservationProviderType(); typed != "docker" {
		t.Fatal("provider type lost", typed)
	}
	for _, test := range []struct {
		ref      sandbox.Reference
		provider *observationProvider
	}{{ra, a}, {rb, b}} {
		target := observationTarget(test.ref, first.InstallationID)
		sample, err := source.Observe(t.Context(), target)
		if err != nil || !reflect.DeepEqual(sample, test.provider.sample) {
			t.Fatal("observation crossed node or changed sample", sample, err)
		}
		test.provider.observationMu.Lock()
		observed := test.provider.targets[0]
		test.provider.observationMu.Unlock()
		target.TokenUsage = nil
		if !reflect.DeepEqual(observed, target) {
			t.Fatal("durable observation target changed", observed, target)
		}
		target.Instance.ComputePhase = "suspended"
		if _, err := source.Observe(t.Context(), target); !errors.Is(err, runtimeobs.ErrNotRunning) {
			t.Fatal("suspended observation did not remain stopped", err)
		}
	}
	if _, err := source.Observe(t.Context(), observationTarget(reference(), first.InstallationID)); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatal("unknown allocation was routed", err)
	}
	for _, p := range []*observationProvider{a, b} {
		p.mu.Lock()
		if p.creates != 0 || p.kills != 0 || p.reads != 0 {
			t.Error("observation invoked lifecycle provider methods")
		}
		p.mu.Unlock()
	}
	stopSecond()
	wait(t, func() bool { return !hub.Online(second.NodeID) })
	if _, err := source.Observe(t.Context(), observationTarget(rb, first.InstallationID)); !errors.Is(err, runtimeobs.ErrUnavailable) {
		t.Fatal("offline source fell back", err)
	}
}

func TestObservationWirePreservesUnavailableAndRejectsMismatchedIdentity(t *testing.T) {
	r := reference()
	target := observationTarget(r, uuid.NewString())
	target.TokenUsage = nil
	q := request{ID: uuid.NewString(), ConnectionID: uuid.NewString(), Operation: "observe", Reference: r, Observation: &target, TimeoutMillis: 1000}
	providers := []struct {
		name     string
		provider sandbox.SandboxProvider
		want     error
	}{
		{"unsupported", &fakeProvider{}, providercontract.ErrUnsupported},
		{"unavailable", &observationProvider{fakeProvider: &fakeProvider{}, err: runtimeobs.ErrUnavailable}, runtimeobs.ErrUnavailable},
		{"stopped", &observationProvider{fakeProvider: &fakeProvider{}, err: runtimeobs.ErrNotRunning}, runtimeobs.ErrNotRunning},
		{"ownership", &observationProvider{fakeProvider: &fakeProvider{}, err: sandbox.ErrOwnership}, sandbox.ErrOwnership},
	}
	for _, test := range providers {
		t.Run(test.name, func(t *testing.T) {
			out := execute(t.Context(), test.provider, q)
			if !errors.Is(responseError(out), test.want) || out.Sample != nil {
				t.Fatal("observation error or missing sample was fabricated", out)
			}
		})
	}
	p := &observationProvider{fakeProvider: &fakeProvider{}}
	for _, mutate := range []func(*runtimeobs.Target){func(t *runtimeobs.Target) { t.EnvironmentID = uuid.NewString() }, func(t *runtimeobs.Target) { t.Instance.AllocationID = uuid.NewString() }, func(t *runtimeobs.Target) { t.TenantID = uuid.NewString() }, func(t *runtimeobs.Target) { t.TokenUsage = &runtimeobs.TokenUsage{} }, func(t *runtimeobs.Target) { t.Mode = runtimeobs.ModeSelfHosted }} {
		invalid := target
		mutate(&invalid)
		q.Observation = &invalid
		if out := execute(t.Context(), p, q); !errors.Is(responseError(out), sandbox.ErrInvalid) {
			t.Fatal("mismatched observation reached provider", out)
		}
	}
	if len(p.targets) != 0 {
		t.Fatal("invalid target was observed")
	}
}
