package sandbox

import (
	"context"
	"maps"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
)

// NewGenerationRouter resolves each allocation's immutable generation for every
// operation. The resolver owns any call fence and releases it after the call.
func NewGenerationRouter(declared providercontract.Operations, resolve func(context.Context, Reference) (SandboxProvider, func(), error)) SandboxProvider {
	return &generationRouter{operations: maps.Clone(declared), resolve: resolve}
}

type generationRouter struct {
	operations providercontract.Operations
	resolve    func(context.Context, Reference) (SandboxProvider, func(), error)
}

func (p *generationRouter) route(ctx context.Context, operation string, ref Reference) (SandboxProvider, func(), error) {
	if err := p.operations[operation].Check(operation); err != nil {
		return nil, nil, err
	}
	return p.resolve(ctx, ref)
}
func (p *generationRouter) Create(ctx context.Context, b Bootstrap) (Info, error) {
	v, done, err := p.route(ctx, "Create", b.Reference)
	if err != nil {
		return Info{}, err
	}
	defer done()
	return v.Create(ctx, b)
}
func (p *generationRouter) GetInfo(ctx context.Context, r Reference) (Info, error) {
	v, done, err := p.route(ctx, "GetInfo", r)
	if err != nil {
		return Info{}, err
	}
	defer done()
	return v.GetInfo(ctx, r)
}
func (p *generationRouter) Renew(ctx context.Context, r Reference) (Info, error) {
	v, done, err := p.route(ctx, "Renew", r)
	if err != nil {
		return Info{}, err
	}
	defer done()
	return v.Renew(ctx, r)
}
func (p *generationRouter) Kill(ctx context.Context, r Reference) error {
	v, done, err := p.route(ctx, "Kill", r)
	if err != nil {
		return err
	}
	defer done()
	return v.Kill(ctx, r)
}

func (p *generationRouter) ProviderOperations() providercontract.Operations {
	return maps.Clone(p.operations)
}
func (p *generationRouter) Observe(ctx context.Context, t runtimeobs.Target) (runtimeobs.Sample, error) {
	v, done, err := p.route(ctx, "Observe", Reference{TenantID: t.TenantID, EnvironmentID: t.EnvironmentID, AllocationID: t.Instance.AllocationID})
	if err != nil {
		return runtimeobs.Sample{}, err
	}
	defer done()
	return v.Observe(ctx, t)
}

func (p *generationRouter) Initial(ctx context.Context, r Reference) (Compute, error) {
	v, done, err := p.route(ctx, "Initial", r)
	if err != nil {
		return Compute{}, err
	}
	defer done()
	return v.Initial(ctx, r)
}
func (p *generationRouter) NewCompute(ctx context.Context, r Reference, g uint64, snapshot *SnapshotIdentity) (Compute, error) {
	v, done, err := p.route(ctx, "NewCompute", r)
	if err != nil {
		return Compute{}, err
	}
	defer done()
	return v.NewCompute(ctx, r, g, snapshot)
}
func (p *generationRouter) GetCompute(ctx context.Context, r Reference, c Compute) (ComputeState, error) {
	v, done, err := p.route(ctx, "GetCompute", r)
	if err != nil {
		return ComputeState{}, err
	}
	defer done()
	return v.GetCompute(ctx, r, c)
}
func (p *generationRouter) Suspend(ctx context.Context, q SuspendRequest) (ComputeState, error) {
	v, done, err := p.route(ctx, "Suspend", q.Reference)
	if err != nil {
		return ComputeState{}, err
	}
	defer done()
	return v.Suspend(ctx, q)
}
func (p *generationRouter) Resume(ctx context.Context, q ResumeRequest) (ComputeState, error) {
	v, done, err := p.route(ctx, "Resume", q.Reference)
	if err != nil {
		return ComputeState{}, err
	}
	defer done()
	return v.Resume(ctx, q)
}
func (p *generationRouter) KillCompute(ctx context.Context, r Reference, c Compute) error {
	v, done, err := p.route(ctx, "KillCompute", r)
	if err != nil {
		return err
	}
	defer done()
	return v.KillCompute(ctx, r, c)
}
func (p *generationRouter) DeleteSnapshot(ctx context.Context, r Reference, snapshot SnapshotIdentity) error {
	v, done, err := p.route(ctx, "DeleteSnapshot", r)
	if err != nil {
		return err
	}
	defer done()
	return v.DeleteSnapshot(ctx, r, snapshot)
}
func (p *generationRouter) ResumeCompute(ctx context.Context, r Reference, c Compute) (ComputeState, error) {
	v, done, err := p.route(ctx, "ResumeCompute", r)
	if err != nil {
		return ComputeState{}, err
	}
	defer done()
	return v.ResumeCompute(ctx, r, c)
}
