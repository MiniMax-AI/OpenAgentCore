package e2b

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func (p *Provider) Initial(_ context.Context, r sandbox.Reference) (sandbox.Compute, error) {
	if !validReference(r) {
		return sandbox.Compute{}, sandbox.ErrInvalid
	}
	return sandbox.Compute{Name: r.AllocationID}, nil
}
func (p *Provider) NewCompute(_ context.Context, r sandbox.Reference, generation uint64, retained *sandbox.RetainedState) (sandbox.Compute, error) {
	if !validReference(r) || retained == nil || sandbox.ValidateRetained(*retained) != nil || retained.Reference != r.AllocationID || retained.SourceName != r.AllocationID || generation == 0 || generation != retained.SourceGeneration+1 {
		return sandbox.Compute{}, sandbox.ErrInvalid
	}
	value := *retained
	return sandbox.Compute{Generation: generation, Name: r.AllocationID, ID: retained.SourceID, RestoredFrom: &value}, nil
}
func (p *Provider) computeCall(ctx context.Context, q Request) (Response, error) {
	deadline, ok := ctx.Deadline()
	if !ok || ctx.Err() != nil {
		return Response{}, sandbox.ErrInvalid
	}
	q.Version, q.Config, q.Deadline = ProtocolVersion, p.config, deadline
	return p.callRequest(ctx, q)
}
func (p *Provider) computeState(ctx context.Context, operation string, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	if c.Name != r.AllocationID {
		return sandbox.ComputeState{}, sandbox.ErrOwnership
	}
	out, err := p.computeCall(ctx, Request{Operation: operation, Reference: r, Compute: &c})
	if err != nil {
		return sandbox.ComputeState{}, err
	}
	if out.State == nil || sandbox.ValidateComputeResult(c, out.State.Compute) != nil {
		return sandbox.ComputeState{}, sandbox.ErrComputeUnconfirmed
	}
	return *out.State, nil
}
func (p *Provider) GetCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	return p.computeState(ctx, "compute_info", r, c)
}
func (p *Provider) RenewCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	return p.computeState(ctx, "compute_renew", r, c)
}
func (p *Provider) ResumeCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	return p.computeState(ctx, "resume_compute", r, c)
}
func (p *Provider) Suspend(ctx context.Context, q sandbox.SuspendRequest) (sandbox.ComputeState, error) {
	if !validID(q.OperationID) || q.Source.Name != q.Reference.AllocationID || q.Source.ID == "" {
		return sandbox.ComputeState{}, sandbox.ErrInvalid
	}
	out, err := p.computeCall(ctx, Request{Operation: "suspend", Reference: q.Reference, Suspend: &q})
	if err != nil {
		return sandbox.ComputeState{}, err
	}
	if out.State == nil {
		return sandbox.ComputeState{}, sandbox.ErrComputeUnconfirmed
	}
	if err = sandbox.ValidateSuspendResult(q, *out.State); err != nil {
		return sandbox.ComputeState{}, err
	}
	if out.State.Retained != nil && out.State.Retained.Reference != q.Reference.AllocationID {
		return sandbox.ComputeState{}, sandbox.ErrOwnership
	}
	return *out.State, nil
}
func (p *Provider) Resume(ctx context.Context, q sandbox.ResumeRequest) (sandbox.ComputeState, error) {
	planned, err := p.NewCompute(ctx, q.Reference, q.Target.Generation, &q.Retained)
	if err != nil || sandbox.ValidateComputeResult(planned, q.Target) != nil || !validID(q.OperationID) {
		return sandbox.ComputeState{}, sandbox.ErrInvalid
	}
	out, err := p.computeCall(ctx, Request{Operation: "resume", Reference: q.Reference, Resume: &q})
	if err != nil {
		return sandbox.ComputeState{}, err
	}
	if out.State == nil || sandbox.ValidateComputeResult(q.Target, out.State.Compute) != nil || out.State.Status != "running" || !out.State.BootstrapComplete {
		return sandbox.ComputeState{}, sandbox.ErrComputeUnconfirmed
	}
	return *out.State, nil
}
func (p *Provider) KillCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) error {
	if c.Name != r.AllocationID || c.ID == "" {
		return sandbox.ErrInvalid
	}
	_, err := p.computeCall(ctx, Request{Operation: "compute_kill", Reference: r, Compute: &c})
	return err
}
func (p *Provider) DeleteRetained(ctx context.Context, r sandbox.Reference, s sandbox.RetainedState) error {
	if sandbox.ValidateRetained(s) != nil || s.Reference != r.AllocationID {
		return sandbox.ErrInvalid
	}
	_, err := p.computeCall(ctx, Request{Operation: "delete_retained", Reference: r, Retained: &s})
	return err
}
func (p *Provider) RunCommandCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute, command sandbox.Command) (sandbox.CommandResult, error) {
	if c.Name != r.AllocationID || c.ID == "" {
		return sandbox.CommandResult{}, sandbox.ErrInvalid
	}
	out, err := p.computeCall(ctx, Request{Operation: "compute_command", Reference: r, Compute: &c, Command: &command})
	if err != nil {
		return sandbox.CommandResult{}, err
	}
	if out.Command == nil {
		return sandbox.CommandResult{}, sandbox.ErrCommandUnconfirmed
	}
	return *out.Command, nil
}
