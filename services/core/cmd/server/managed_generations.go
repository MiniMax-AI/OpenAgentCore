package main

import (
	"context"
	"errors"
	"maps"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// Every facade, including already running lifecycles, resolves the allocation's
// immutable specification and current credential. There is no mutable provider
// map to unload and no current-generation fallback for a missing historical row.
type generationRouter struct {
	setup        *managedSetup
	operations   providercontract.Operations
	providerType string
}

func (p *generationRouter) route(ctx context.Context, r sandbox.Reference) (sandbox.SandboxProvider, func(), error) {
	release, err := p.setup.providerCalls.Enter(ctx)
	if err != nil {
		return nil, nil, err
	}
	setup, err := p.setup.deployment.AllocationSetup(ctx, r)
	if err != nil {
		release()
		return nil, nil, err
	}
	provider, err := p.setup.provider(setup)
	if err != nil {
		release()
		return nil, nil, err
	}
	return provider, release, nil
}
func (p *generationRouter) Create(ctx context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	v, done, err := p.route(ctx, b.Reference)
	if err != nil {
		return sandbox.Info{}, err
	}
	defer done()
	return v.Create(ctx, b)
}
func (p *generationRouter) GetInfo(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	v, done, err := p.route(ctx, r)
	if err != nil {
		return sandbox.Info{}, err
	}
	defer done()
	return v.GetInfo(ctx, r)
}
func (p *generationRouter) Renew(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	v, done, err := p.route(ctx, r)
	if err != nil {
		return sandbox.Info{}, err
	}
	defer done()
	return v.Renew(ctx, r)
}
func (p *generationRouter) Kill(ctx context.Context, r sandbox.Reference) error {
	v, done, err := p.route(ctx, r)
	if err != nil {
		return err
	}
	defer done()
	return v.Kill(ctx, r)
}
func (p *generationRouter) RunCommand(ctx context.Context, r sandbox.Reference, c sandbox.Command) (sandbox.CommandResult, error) {
	v, done, err := p.route(ctx, r)
	if err != nil {
		return sandbox.CommandResult{}, err
	}
	defer done()
	return v.RunCommand(ctx, r, c)
}

type observedGenerationRouter struct{ *generationRouter }

func (p *observedGenerationRouter) ObservationProviderType() string { return p.providerType }
func (p *observedGenerationRouter) ResolveObservationSource(context.Context) (runtimeobs.Source, error) {
	return p, nil
}

func (p *generationRouter) ProviderOperations() providercontract.Operations {
	return maps.Clone(p.operations)
}

func (p *observedGenerationRouter) Observe(ctx context.Context, t runtimeobs.Target) (runtimeobs.Sample, error) {
	v, done, err := p.route(ctx, sandbox.Reference{TenantID: t.TenantID, EnvironmentID: t.EnvironmentID, AllocationID: t.Instance.AllocationID})
	if err != nil {
		return runtimeobs.Sample{}, err
	}
	defer done()
	if err := providercontract.Require(v, "Observe"); err != nil {
		return runtimeobs.Sample{}, err
	}
	source, ok := v.(runtimeobs.Source)
	if !ok {
		return runtimeobs.Sample{}, providercontract.ErrContract
	}
	if source.ObservationProviderType() != p.providerType {
		return runtimeobs.Sample{}, providercontract.ErrContract
	}
	return source.Observe(ctx, t)
}

// routeGenerations routes a direct provider's allocations through their own
// generations. Node providers route through the node transport instead.
func (s *managedSetup) routeGenerations(candidate execution.PreparedRuntimeDeployment, setup deployment.Setup) (execution.PreparedRuntimeDeployment, error) {
	if setup.Mode == "nodes" {
		return candidate, nil
	}
	router := &generationRouter{setup: s, providerType: candidate.Config.Provider.(runtimeobs.Source).ObservationProviderType(), operations: candidate.Config.Provider.ProviderOperations()}
	router.operations["ObserveBatch"] = providercontract.Support{State: providercontract.Unsupported, Reason: "allocations_require_individual_generation_routing"}
	router.operations["DiscoverSelection"] = providercontract.Support{State: providercontract.Unsupported, Reason: "generation_router_does_not_discover_configuration"}
	router.operations["VerifyCredential"] = providercontract.Support{State: providercontract.Unsupported, Reason: "generation_router_does_not_verify_configuration"}
	candidate.Config.Provider = &observedGenerationRouter{router}
	if err := sandbox.ValidateProvider(candidate.Config.Provider); err != nil {
		return execution.PreparedRuntimeDeployment{}, err
	}
	if !setup.UsesCredential {
		return candidate, nil
	}
	candidate.FenceCredential = func(ctx context.Context) (func(), error) {
		release, err := s.providerCalls.Fence(ctx)
		if err != nil {
			return nil, sandbox.ErrConfigurationUnconfirmed
		}
		return release, nil
	}
	candidate.VerifyCredential = func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		// Preserve the committed credential until its ownership anchor is verified.
		// Public template readability cannot establish which team owns a deployment.
		verify := func(value deployment.Setup, refs []sandbox.Reference) error {
			value.InstallationID = setup.InstallationID
			provider, err := s.provider(value)
			if err != nil {
				return err
			}
			if err := providercontract.Require(provider, "VerifyCredential"); err != nil {
				return err
			}
			verifier, ok := provider.(sandbox.CredentialVerifier)
			if !ok {
				return sandbox.ErrInvalid
			}
			return verifier.VerifyCredential(ctx, refs)
		}
		current, err := s.deployment.Setup(ctx)
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
			value, err := s.deployment.WithCredential(value, setup)
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
			page, err := s.deployment.GenerationPage(ctx, after)
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
			page, err := s.allocations.CredentialAllocations(ctx, after)
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
	return candidate, nil
}

// A batch can contain allocations from different endpoint generations. Let the
// observation worker route each allocation through its immutable generation.
func (p *observedGenerationRouter) ObserveBatch(_ context.Context, _ []runtimeobs.Target) ([]runtimeobs.BatchResult, error) {
	return nil, &providercontract.UnsupportedError{Operation: "ObserveBatch", Reason: "allocations_require_individual_generation_routing"}
}
