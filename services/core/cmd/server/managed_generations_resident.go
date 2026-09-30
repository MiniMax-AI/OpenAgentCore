package main

import (
	"context"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// Resident operations retain the allocation generation and credential fence.

func (p *generationRouter) PauseResident(ctx context.Context, ref sandbox.Reference) (sandbox.Info, error) {
	if err := providercontract.Require(p, "PauseResident"); err != nil {
		return sandbox.Info{}, err
	}
	provider, done, err := p.route(ctx, ref)
	if err != nil {
		return sandbox.Info{}, err
	}
	defer done()
	resident, err := sandbox.ResidentPause(provider)
	if err != nil {
		return sandbox.Info{}, err
	}
	return resident.PauseResident(ctx, ref)
}

func (p *generationRouter) ResumeResident(ctx context.Context, ref sandbox.Reference) (sandbox.Info, error) {
	if err := providercontract.Require(p, "ResumeResident"); err != nil {
		return sandbox.Info{}, err
	}
	provider, done, err := p.route(ctx, ref)
	if err != nil {
		return sandbox.Info{}, err
	}
	defer done()
	resident, err := sandbox.ResidentPause(provider)
	if err != nil {
		return sandbox.Info{}, err
	}
	return resident.ResumeResident(ctx, ref)
}

var _ sandbox.ResidentPauseProvider = (*generationRouter)(nil)
