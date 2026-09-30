package main

import (
	"context"
	"errors"
	"maps"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

type generationStore interface {
	GetSandboxAllocationSetup(context.Context, sandbox.Reference) (store.SandboxSetup, error)
	GetSandboxSetup(context.Context) (store.SandboxSetup, error)
	SandboxGenerationPage(context.Context, int64) ([]store.SandboxSetup, error)
	SandboxCredentialAllocationPage(context.Context, string) ([]store.RuntimeAllocation, error)
}

// Every facade, including already running lifecycles, resolves the allocation's
// immutable specification and current credential. There is no mutable provider
// map to unload and no current-generation fallback for a missing historical row.
type generationRouter struct {
	setup        *managedSetup
	store        generationStore
	operations   providercontract.Operations
	providerType string
}

func (p *generationRouter) route(ctx context.Context, r sandbox.Reference) (sandbox.SandboxProvider, func(), error) {
	release, err := p.setup.providerCalls.Enter(ctx)
	if err != nil {
		return nil, nil, err
	}
	setup, err := p.store.GetSandboxAllocationSetup(ctx, r)
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

func (s *managedSetup) routeGenerations(candidate execution.PreparedRuntimeDeployment, setup store.SandboxSetup) (execution.PreparedRuntimeDeployment, error) {
	usesCredential, err := providers.UsesCredential(setup.Provider)
	if err != nil {
		return execution.PreparedRuntimeDeployment{}, err
	}
	adapter, err := providers.Lookup(setup.Provider)
	if err != nil {
		return execution.PreparedRuntimeDeployment{}, err
	}
	if adapter.Mode == "nodes" {
		return candidate, nil
	}
	db, ok := s.store.(generationStore)
	if !ok {
		return execution.PreparedRuntimeDeployment{}, errors.New("sandbox generation store is unavailable")
	}
	router := &generationRouter{setup: s, store: db, providerType: candidate.Config.Provider.(runtimeobs.Source).ObservationProviderType(), operations: candidate.Config.Provider.ProviderOperations()}
	router.operations["ObserveBatch"] = providercontract.Support{State: providercontract.Unsupported, Reason: "allocations_require_individual_generation_routing"}
	router.operations["DiscoverSelection"] = providercontract.Support{State: providercontract.Unsupported, Reason: "generation_router_does_not_discover_configuration"}
	router.operations["VerifyCredential"] = providercontract.Support{State: providercontract.Unsupported, Reason: "generation_router_does_not_verify_configuration"}
	candidate.Config.Provider = &observedGenerationRouter{router}
	if err := sandbox.ValidateProvider(candidate.Config.Provider); err != nil {
		return execution.PreparedRuntimeDeployment{}, err
	}
	if !usesCredential {
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
		verify := func(value store.SandboxSetup, refs []sandbox.Reference) error {
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
		current, err := db.GetSandboxSetup(ctx)
		if err != nil {
			return err
		}
		if current.Provider != setup.Provider {
			return &store.SandboxResetRequiredError{CurrentProvider: current.Provider, RequestedProvider: setup.Provider}
		}
		if err := verify(current, nil); err != nil {
			if errors.Is(err, sandbox.ErrCredentialRejected) || errors.Is(err, sandbox.ErrCredentialOwnership) {
				// A revoked legacy key or a public template outside its team cannot
				// anchor ownership. This says nothing about the candidate key's validity.
				return &store.SandboxResetRequiredError{CurrentProvider: setup.Provider, RequestedProvider: setup.Provider}
			}
			return err
		}
		withCandidateKey := func(value store.SandboxSetup, refs []sandbox.Reference) error {
			selection, err := providers.WithCredential(sandbox.Selection{Provider: value.Provider, DeploymentSpec: value.Specification, Configuration: value.Configuration}, sandbox.Selection{Provider: setup.Provider, DeploymentSpec: setup.Specification, Configuration: setup.Configuration})
			if err != nil {
				return err
			}
			value.Configuration = selection.Configuration
			return verify(value, refs)
		}
		if err := withCandidateKey(current, nil); err != nil {
			return err
		}
		if err := verify(setup, nil); err != nil {
			return err
		}
		generations := map[uint64]store.SandboxSetup{current.Generation: current}
		for after := int64(-1); ; {
			page, err := db.SandboxGenerationPage(ctx, after)
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
			page, err := db.SandboxCredentialAllocationPage(ctx, after)
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
