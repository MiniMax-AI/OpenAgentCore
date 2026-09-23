package node

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

var _ runtimeobs.Source = (*provider)(nil)

func (p *provider) ObservationProviderType() string { return p.kind }

func observationReference(target runtimeobs.Target) sandbox.Reference {
	return sandbox.Reference{TenantID: target.TenantID, EnvironmentID: target.EnvironmentID, AllocationID: target.Instance.AllocationID}
}

func validObservation(target runtimeobs.Target, reference sandbox.Reference) bool {
	return target.Mode == runtimeobs.ModeManaged && validID(target.SessionID) &&
		validID(target.Instance.ProviderKey) && observationReference(target) == reference && target.TokenUsage == nil
}

// Observe uses the allocation's existing node resolver and never changes its
// compute state. Session token counters stay in Core, outside provider telemetry.
func (p *provider) Observe(ctx context.Context, target runtimeobs.Target) (runtimeobs.Sample, error) {
	target.TokenUsage = nil
	out, err := p.call(ctx, request{Operation: "observe", Reference: observationReference(target), Observation: &target})
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) &&
			(errors.Is(err, ErrUnavailable) || errors.Is(err, sandbox.ErrComputeUnconfirmed)) {
			return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
		}
		return runtimeobs.Sample{}, err
	}
	if out.Sample == nil {
		return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
	}
	return *out.Sample, nil
}

func observeProvider(ctx context.Context, provider sandbox.Provider, target runtimeobs.Target) (runtimeobs.Sample, error) {
	source, ok := provider.(runtimeobs.Source)
	if !ok {
		return runtimeobs.Sample{}, runtimeobs.ErrUnavailable
	}
	return source.Observe(ctx, target)
}
