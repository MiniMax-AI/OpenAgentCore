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
	return v.Initial(ctx, r)
}
func (p *generationRouter) NewCompute(ctx context.Context, r sandbox.Reference, g uint64, snapshot *sandbox.SnapshotIdentity) (sandbox.Compute, error) {
	if err := providercontract.Require(p, "NewCompute"); err != nil {
		return sandbox.Compute{}, err
	}
	v, done, err := p.route(ctx, r)
	if err != nil {
		return sandbox.Compute{}, err
	}
	defer done()
	return v.NewCompute(ctx, r, g, snapshot)
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
	return v.GetCompute(ctx, r, c)
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
	return v.Suspend(ctx, q)
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
	return v.Resume(ctx, q)
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
	return v.KillCompute(ctx, r, c)
}
func (p *generationRouter) DeleteSnapshot(ctx context.Context, r sandbox.Reference, snapshot sandbox.SnapshotIdentity) error {
	if err := providercontract.Require(p, "DeleteSnapshot"); err != nil {
		return err
	}
	v, done, err := p.route(ctx, r)
	if err != nil {
		return err
	}
	defer done()
	return v.DeleteSnapshot(ctx, r, snapshot)
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
	return v.ResumeCompute(ctx, r, c)
}
