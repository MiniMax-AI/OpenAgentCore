package execution

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// Retained facades read allocation-owned specifications with the current
// credential inside the same fence as the native call.
func (s *runtimeManager) directProvider(ctx context.Context, r sandbox.Reference) (sandbox.SandboxProvider, func(), error) {
	release, err := s.providerCalls.Enter(ctx)
	if err != nil {
		return nil, nil, err
	}
	setup, err := s.setups.AllocationSetup(ctx, r)
	if err != nil {
		release()
		return nil, nil, err
	}
	provider, err := s.providers.BuildDirect(s.direct(setup))
	if err != nil {
		release()
		return nil, nil, err
	}
	return provider, release, nil
}
func (s *runtimeManager) verifyCredential(ctx context.Context, setup deployment.Setup) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Preserve the committed credential until its ownership anchor is verified.
	// Public template readability cannot establish which team owns a deployment.
	verify := func(value deployment.Setup, refs []sandbox.Reference) error {
		value.InstallationID = setup.InstallationID
		return s.providers.VerifyCredential(ctx, s.direct(value), refs)
	}
	current, err := s.setups.Setup(ctx)
	if err != nil {
		return err
	}
	if current.Provider != setup.Provider {
		return &deployment.ResetRequiredError{CurrentProvider: current.Provider, RequestedProvider: setup.Provider}
	}
	if err := verify(current, nil); err != nil {
		if errors.Is(err, sandbox.ErrCredentialRejected) || errors.Is(err, sandbox.ErrCredentialOwnership) {
			// A revoked legacy key or a public template outside its team cannot
			// anchor ownership. This says nothing about the candidate key's validity.
			return &deployment.ResetRequiredError{CurrentProvider: setup.Provider, RequestedProvider: setup.Provider}
		}
		return err
	}
	withCandidateKey := func(value deployment.Setup, refs []sandbox.Reference) error {
		value, err := s.setups.WithCredential(value, setup)
		if err != nil {
			return err
		}
		return verify(value, refs)
	}
	if err := withCandidateKey(current, nil); err != nil {
		return err
	}
	if err := verify(setup, nil); err != nil {
		return err
	}
	generations := map[uint64]deployment.Setup{current.Generation: current}
	for after := int64(-1); ; {
		page, err := s.setups.GenerationPage(ctx, after)
		if err != nil {
			return err
		}
		for _, g := range page {
			generations[g.Generation] = g
			if err := withCandidateKey(g, nil); err != nil {
				return err
			}
			after = int64(g.Generation)
		}
		if len(page) < 32 {
			break
		}
	}
	for after := ""; ; {
		page, err := s.deploymentReader.CredentialAllocations(ctx, after)
		if err != nil {
			return err
		}
		refsByGeneration := make(map[uint64][]sandbox.Reference)
		for _, a := range page {
			refsByGeneration[a.DeploymentGeneration] = append(refsByGeneration[a.DeploymentGeneration], sandbox.Reference{TenantID: a.TenantID, EnvironmentID: a.EnvironmentID, AllocationID: a.ID})
			after = a.ID
		}
		for generation, refs := range refsByGeneration {
			owner, ok := generations[generation]
			if !ok {
				return sandbox.ErrConfigurationUnconfirmed
			}
			if err := withCandidateKey(owner, refs); err != nil {
				return err
			}
		}
		if len(page) < 32 {
			break
		}
	}
	return nil
}
