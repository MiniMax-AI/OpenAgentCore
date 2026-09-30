package main

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

// Only providers that advertise resident pause receive this facade. Every
// operation keeps the allocation's immutable generation and credential fence.
type residentGenerationRouter struct{ *generationRouter }

func (p *residentGenerationRouter) Pause(ctx context.Context, ref sandbox.Reference) (sandbox.Info, error) {
	provider, done, err := p.route(ctx, ref)
	if err != nil {
		return sandbox.Info{}, err
	}
	defer done()
	resident, ok := provider.(sandbox.ResidentPauseProvider)
	if !ok {
		return sandbox.Info{}, sandbox.ErrInvalid
	}
	return resident.Pause(ctx, ref)
}

func (p *residentGenerationRouter) Resume(ctx context.Context, ref sandbox.Reference) (sandbox.Info, error) {
	provider, done, err := p.route(ctx, ref)
	if err != nil {
		return sandbox.Info{}, err
	}
	defer done()
	resident, ok := provider.(sandbox.ResidentPauseProvider)
	if !ok {
		return sandbox.Info{}, sandbox.ErrInvalid
	}
	return resident.Resume(ctx, ref)
}

type observedResidentGenerationRouter struct{ *residentGenerationRouter }

func (p *observedResidentGenerationRouter) Observe(ctx context.Context, target runtimeobs.Target) (runtimeobs.Sample, error) {
	return (&observedGenerationRouter{p.generationRouter}).Observe(ctx, target)
}

var _ sandbox.ResidentPauseProvider = (*residentGenerationRouter)(nil)
var _ sandbox.ResidentPauseProvider = (*observedResidentGenerationRouter)(nil)
var _ runtimeobs.Source = (*observedResidentGenerationRouter)(nil)
