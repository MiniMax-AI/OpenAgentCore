package runtimeobs

import (
	"context"
	"errors"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
)

type selectingSource struct {
	source       Source
	providerType string
	err          error
	calls        int
}

func (s *selectingSource) load(context.Context) (Source, string, error) {
	s.calls++
	return s.source, s.providerType, s.err
}

func observableTarget() fixedResolver {
	return fixedResolver{target: Target{EnvironmentID: "environment", Mode: v1.RuntimeModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}}
}

func TestUnavailableAndNilSourceSelection(t *testing.T) {
	resolver := &selectingSource{err: ErrUnavailable}
	service, err := NewService(observableTarget(), resolver.load)
	if err != nil || resolver.calls != 0 {
		t.Fatal("registration resolved unconfigured provider", err)
	}
	observation, err := service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != v1.RuntimeStatusUnavailable || observation.Reason != "sample_unavailable" || observation.ProviderType != "" {
		t.Fatal(observation, err)
	}
	resolver.err = nil
	if _, err := service.ObserveSession(t.Context(), "tenant", "session"); !errors.Is(err, providercontract.ErrContract) {
		t.Fatal("nil source accepted", err)
	}
}

type reconfiguringSource struct {
	*fixedSource
	resolver *selectingSource
	next     Source
}

func (s *reconfiguringSource) Observe(ctx context.Context, target Target) (Sample, error) {
	s.resolver.source, s.resolver.providerType = s.next, "next"
	return s.fixedSource.Observe(ctx, target)
}

func TestSourceSelectionStaysBoundForWholePage(t *testing.T) {
	now := time.Now()
	next := &fixedSource{sample: Sample{ObservedAt: now}}
	resolver := &selectingSource{providerType: "previous"}
	previous := &reconfiguringSource{fixedSource: &fixedSource{sample: Sample{ObservedAt: now}}, resolver: resolver, next: next}
	resolver.source = previous
	service, err := NewService(observableTarget(), resolver.load)
	if err != nil {
		t.Fatal(err)
	}
	sessions := make([]SessionIdentity, 3)
	for index := range sessions {
		sessions[index] = SessionIdentity{TenantID: "tenant", SessionID: "session"}
	}
	observations, errs := service.ObserveSessions(t.Context(), sessions, PageOptions{Concurrency: 1})
	for index, observation := range observations {
		if errs[index] != nil || observation.ProviderType != "previous" || observation.Status != v1.RuntimeStatusObserved {
			t.Fatal(index, observation, errs[index])
		}
	}
	if resolver.calls != 1 || previous.calls != len(sessions) || next.calls != 0 {
		t.Fatalf("selection/read counts %d / %d / %d", resolver.calls, previous.calls, next.calls)
	}
	observation, err := service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.ProviderType != "next" || next.calls != 1 || resolver.calls != 2 {
		t.Fatal("later page did not select new provider", observation, err)
	}
}
