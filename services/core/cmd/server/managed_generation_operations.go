package main

import (
	"context"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func (p *generationRouter) Initial(ctx context.Context, r sandbox.Reference) (sandbox.Compute, error) {
	if err := providercontract.Require(p, "Initial"); err != nil {
		return sandbox.Compute{}, err
	}
	v, done, err := p.route(ctx, r)
	if err != nil {
		return sandbox.Compute{}, err
	}
	defer done()
	cp, err := sandbox.Suspension(v)
	if err != nil {
		return sandbox.Compute{}, err
	}
	return cp.Initial(ctx, r)
}
func (p *generationRouter) NewCompute(ctx context.Context, r sandbox.Reference, g uint64, snapshot *sandbox.RetainedState) (sandbox.Compute, error) {
	if err := providercontract.Require(p, "NewCompute"); err != nil {
		return sandbox.Compute{}, err
	}
	v, done, err := p.route(ctx, r)
	if err != nil {
		return sandbox.Compute{}, err
	}
	defer done()
	cp, err := sandbox.Suspension(v)
	if err != nil {
		return sandbox.Compute{}, err
	}
	return cp.NewCompute(ctx, r, g, snapshot)
}
func (p *generationRouter) GetCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	if err := providercontract.Require(p, "GetCompute"); err != nil {
		return sandbox.ComputeState{}, err
	}
	v, done, err := p.route(ctx, r)
	if err != nil {
		return sandbox.ComputeState{}, err
	}
	defer done()
	cp, err := sandbox.Suspension(v)
	if err != nil {
		return sandbox.ComputeState{}, err
	}
	return cp.GetCompute(ctx, r, c)
}
func (p *generationRouter) Suspend(ctx context.Context, q sandbox.SuspendRequest) (sandbox.ComputeState, error) {
	if err := providercontract.Require(p, "Suspend"); err != nil {
		return sandbox.ComputeState{}, err
	}
	v, done, err := p.route(ctx, q.Reference)
	if err != nil {
		return sandbox.ComputeState{}, err
	}
	defer done()
	cp, err := sandbox.Suspension(v)
	if err != nil {
		return sandbox.ComputeState{}, err
	}
	return cp.Suspend(ctx, q)
}
func (p *generationRouter) Resume(ctx context.Context, q sandbox.ResumeRequest) (sandbox.ComputeState, error) {
	if err := providercontract.Require(p, "Resume"); err != nil {
		return sandbox.ComputeState{}, err
	}
	v, done, err := p.route(ctx, q.Reference)
	if err != nil {
		return sandbox.ComputeState{}, err
	}
	defer done()
	cp, err := sandbox.Suspension(v)
	if err != nil {
		return sandbox.ComputeState{}, err
	}
	return cp.Resume(ctx, q)
}
func (p *generationRouter) KillCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) error {
	if err := providercontract.Require(p, "KillCompute"); err != nil {
		return err
	}
	v, done, err := p.route(ctx, r)
	if err != nil {
		return err
	}
	defer done()
	cp, err := sandbox.Suspension(v)
	if err != nil {
		return err
	}
	return cp.KillCompute(ctx, r, c)
}
func (p *generationRouter) DeleteRetained(ctx context.Context, r sandbox.Reference, snapshot sandbox.RetainedState) error {
	if err := providercontract.Require(p, "DeleteRetained"); err != nil {
		return err
	}
	v, done, err := p.route(ctx, r)
	if err != nil {
		return err
	}
	defer done()
	cp, err := sandbox.Suspension(v)
	if err != nil {
		return err
	}
	return cp.DeleteRetained(ctx, r, snapshot)
}
func (p *generationRouter) RunCommandCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute, command sandbox.Command) (sandbox.CommandResult, error) {
	if err := providercontract.Require(p, "RunCommandCompute"); err != nil {
		return sandbox.CommandResult{}, err
	}
	v, done, err := p.route(ctx, r)
	if err != nil {
		return sandbox.CommandResult{}, err
	}
	defer done()
	cp, err := sandbox.Suspension(v)
	if err != nil {
		return sandbox.CommandResult{}, err
	}
	return cp.RunCommandCompute(ctx, r, c, command)
}
func (p *generationRouter) ResumeCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	if err := providercontract.Require(p, "ResumeCompute"); err != nil {
		return sandbox.ComputeState{}, err
	}
	v, done, err := p.route(ctx, r)
	if err != nil {
		return sandbox.ComputeState{}, err
	}
	defer done()
	cp, err := sandbox.Suspension(v)
	if err != nil {
		return sandbox.ComputeState{}, err
	}
	return cp.ResumeCompute(ctx, r, c)
}
func (*generationRouter) DiscoverSelection(context.Context, sandbox.Selection) (sandbox.Selection, error) {
	return sandbox.Selection{}, &providercontract.UnsupportedError{Operation: "DiscoverSelection", Reason: "generation_router_does_not_discover_configuration"}
}
func (*generationRouter) VerifyCredential(context.Context, []sandbox.Reference) error {
	return &providercontract.UnsupportedError{Operation: "VerifyCredential", Reason: "generation_router_does_not_verify_configuration"}
}

func (p *generationRouter) RenewCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	if err := providercontract.Require(p, "RenewCompute"); err != nil {
		return sandbox.ComputeState{}, err
	}
	v, done, err := p.route(ctx, r)
	if err != nil {
		return sandbox.ComputeState{}, err
	}
	defer done()
	cp, err := sandbox.Suspension(v)
	if err != nil {
		return sandbox.ComputeState{}, err
	}
	return cp.RenewCompute(ctx, r, c)
}
