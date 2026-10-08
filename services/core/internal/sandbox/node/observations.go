package node

import (
	"context"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func observationReference(target runtimeobs.Target) sandbox.Reference {
	return sandbox.Reference{TenantID: target.TenantID, EnvironmentID: target.EnvironmentID, AllocationID: target.Instance.AllocationID}
}

func validObservation(target runtimeobs.Target, reference sandbox.Reference) bool {
	return target.Mode == v1.RuntimeModeManaged && validID(target.SessionID) &&
		validID(target.Instance.ProviderKey) && observationReference(target) == reference && target.TokenUsage == nil
}

// Observe reads the bound node and generation and never changes their
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
