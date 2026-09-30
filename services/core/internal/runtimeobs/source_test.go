package runtimeobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
)

type selectingSource struct {
	source  Source
	err     error
	calls   int
	support providercontract.Support
}

func (s *selectingSource) ProviderOperations() providercontract.Operations {
	return providercontract.Operations{"ResolveObservationSource": s.support}
}
func (s *selectingSource) ResolveObservationSource(context.Context) (Source, error) {
	s.calls++
	return s.source, s.err
}

type identityDeclaration struct {
	typedSource
	support providercontract.Support
}

func (s identityDeclaration) ProviderOperations() providercontract.Operations {
	operations := s.typedSource.ProviderOperations()
	operations["ObservationProviderType"] = s.support
	return operations
}

func TestSourceBindingRejectsInvalidIdentityBeforeReadOrExport(t *testing.T) {
	for _, test := range []struct {
		name, identity string
		support        providercontract.Support
	}{
		{"empty", "", providercontract.Support{State: providercontract.Supported}},
		{"unsafe", "secret:provider", providercontract.Support{State: providercontract.Supported}},
		{"undeclared", "docker", providercontract.Support{}},
		{"unsupported", "docker", providercontract.Support{State: providercontract.Unsupported, Reason: "missing_identity"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := identityDeclaration{typedSource: typedSource{fixedSource: &fixedSource{}, providerType: test.identity}, support: test.support}
			resolver := &selectingSource{source: source, support: providercontract.Support{State: providercontract.Supported}}
			records := make(chan ExportRecord, 1)
			service, err := NewService(observableTarget(), map[string]SourceResolver{"provider": resolver}, WithExporter(channelExporter{records: records}, ExportOptions{}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.ObserveSession(t.Context(), "tenant", "session"); !errors.Is(err, providercontract.ErrContract) {
				t.Fatalf("invalid identity accepted: %v", err)
			}
			if source.calls != 0 {
				t.Fatal("invalid identity reached provider read")
			}
			if err := service.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(records) != 0 {
				t.Fatal("invalid identity reached export")
			}
		})
	}
}

func observableTarget() fixedResolver {
	return fixedResolver{target: Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}}
}

func TestSourceResolverRegistrationAndUnavailableSelection(t *testing.T) {
	for _, support := range []providercontract.Support{{}, {State: providercontract.Unsupported, Reason: "no_source"}} {
		resolver := &selectingSource{support: support}
		if _, err := NewService(observableTarget(), map[string]SourceResolver{"provider": resolver}); !errors.Is(err, providercontract.ErrContract) || resolver.calls != 0 {
			t.Fatal("invalid resolver registration accepted or performed I/O", err)
		}
	}
	resolver := &selectingSource{err: ErrUnavailable, support: providercontract.Support{State: providercontract.Supported}}
	service, err := NewService(observableTarget(), map[string]SourceResolver{"provider": resolver})
	if err != nil || resolver.calls != 0 {
		t.Fatal("registration resolved unconfigured provider", err)
	}
	observation, err := service.ObserveSession(t.Context(), "tenant", "session")
	if err != nil || observation.Status != StatusUnavailable || observation.Reason != "sample_unavailable" || observation.ProviderType != "" {
		t.Fatal(observation, err)
	}
	var nilSource *fixedSource
	resolver.err, resolver.source = nil, nilSource
	if _, err := service.ObserveSession(t.Context(), "tenant", "session"); !errors.Is(err, providercontract.ErrContract) {
		t.Fatal("typed nil source accepted", err)
	}
}

type reconfiguringSource struct {
	typedSource
	resolver *selectingSource
	next     Source
}

func (s *reconfiguringSource) Observe(ctx context.Context, target Target) (Sample, error) {
	s.resolver.source = s.next
	return s.typedSource.Observe(ctx, target)
}

func TestSourceSelectionStaysBoundForWholePage(t *testing.T) {
	now := time.Now()
	next := typedSource{fixedSource: &fixedSource{sample: Sample{ObservedAt: now}}, providerType: "next"}
	resolver := &selectingSource{support: providercontract.Support{State: providercontract.Supported}}
	previous := &reconfiguringSource{typedSource: typedSource{fixedSource: &fixedSource{sample: Sample{ObservedAt: now}}, providerType: "previous"}, resolver: resolver, next: next}
	resolver.source = previous
	service, err := NewService(observableTarget(), map[string]SourceResolver{"provider": resolver})
	if err != nil {
		t.Fatal(err)
	}
	sessions := make([]SessionIdentity, MaxBatchTargets+1)
	for index := range sessions {
		sessions[index] = SessionIdentity{TenantID: "tenant", SessionID: "session"}
	}
	observations, errs := service.ObserveSessions(t.Context(), sessions, PageOptions{Concurrency: 1})
	for index, observation := range observations {
		if errs[index] != nil || observation.ProviderType != "previous" || observation.Status != StatusObserved {
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
