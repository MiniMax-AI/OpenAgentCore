package node

import (
	"context"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

type provider struct {
	hub     *Hub
	resolve Resolver
	kind    string
}
type checkpointProvider struct{ *provider }

var _ sandbox.Provider = (*provider)(nil)
var _ sandbox.CheckpointProvider = (*checkpointProvider)(nil)

// Provider advertises checkpoint support only for a deployment using microsandbox.
func (h *Hub) Provider(kind string, resolve Resolver) sandbox.Provider {
	p := &provider{hub: h, resolve: resolve, kind: kind}
	if kind == "microsandbox" {
		return &checkpointProvider{p}
	}
	return p
}
func (h *Hub) Proxy(id, kind string) sandbox.Provider {
	return h.Provider(kind, func(context.Context, sandbox.Reference) (string, error) { return id, nil })
}
func (p *provider) call(ctx context.Context, q request) (response, error) {
	id, err := p.resolve(ctx, q.Reference)
	if err != nil {
		return response{}, err
	}
	if !validID(id) {
		return response{}, sandbox.ErrOwnership
	}
	return p.hub.call(ctx, id, q)
}
func (p *provider) info(ctx context.Context, q request) (sandbox.Info, error) {
	r, e := p.call(ctx, q)
	if e != nil {
		if creationSettled(r.Info, q.Reference) {
			return *r.Info, e
		}
		return sandbox.Info{}, e
	}
	if r.Info == nil || r.Info.Reference != q.Reference || r.Info.ProviderID == "" && !creationSettled(r.Info, q.Reference) {
		return sandbox.Info{}, sandbox.ErrComputeUnconfirmed
	}
	return *r.Info, nil
}

// A failed operation can still carry a provider's explicit creation receipt.
// Keep its original error: settlement is cleanup evidence, not readiness.
func creationSettled(info *sandbox.Info, ref sandbox.Reference) bool {
	if info == nil || info.Reference != ref || !info.CreateSettled {
		return false
	}
	if info.State == "absent" {
		return info.ProviderID == "" && !info.BootstrapComplete
	}
	return info.ProviderID != ""
}
func (p *provider) Create(ctx context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	return p.info(ctx, request{Operation: "create", Reference: b.Reference, Bootstrap: &b})
}
func (p *provider) GetInfo(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	return p.info(ctx, request{Operation: "info", Reference: r})
}
func (p *provider) Renew(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	return p.info(ctx, request{Operation: "renew", Reference: r})
}
func (p *provider) Kill(ctx context.Context, r sandbox.Reference) error {
	_, e := p.call(ctx, request{Operation: "kill", Reference: r})
	return e
}
func (p *provider) command(ctx context.Context, q request) (sandbox.CommandResult, error) {
	r, e := p.call(ctx, q)
	if e != nil {
		return sandbox.CommandResult{}, e
	}
	if r.Command == nil || len(r.Command.Stdout) > 1024*1024 || len(r.Command.Stderr) > 1024*1024 {
		return sandbox.CommandResult{}, sandbox.ErrCommandUnconfirmed
	}
	return *r.Command, nil
}
func (p *provider) RunCommand(ctx context.Context, r sandbox.Reference, c sandbox.Command) (sandbox.CommandResult, error) {
	return p.command(ctx, request{Operation: "command", Reference: r, Command: &c})
}
func (p *checkpointProvider) Initial(ctx context.Context, r sandbox.Reference) (sandbox.Compute, error) {
	out, e := p.call(ctx, request{Operation: "initial", Reference: r})
	if e != nil {
		return sandbox.Compute{}, e
	}
	if out.Compute == nil {
		return sandbox.Compute{}, sandbox.ErrComputeUnconfirmed
	}
	return *out.Compute, nil
}
func (p *checkpointProvider) NewCompute(ctx context.Context, r sandbox.Reference, g uint64, s *sandbox.SnapshotIdentity) (sandbox.Compute, error) {
	out, e := p.call(ctx, request{Operation: "new_compute", Reference: r, Generation: g, Snapshot: s})
	if e != nil {
		return sandbox.Compute{}, e
	}
	if out.Compute == nil {
		return sandbox.Compute{}, sandbox.ErrComputeUnconfirmed
	}
	return *out.Compute, nil
}
func (p *checkpointProvider) state(ctx context.Context, q request) (sandbox.ComputeState, error) {
	r, e := p.call(ctx, q)
	if e != nil {
		return sandbox.ComputeState{}, e
	}
	if r.State == nil || r.State.Compute.ID == "" {
		return sandbox.ComputeState{}, sandbox.ErrComputeUnconfirmed
	}
	return *r.State, nil
}
func (p *checkpointProvider) GetCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	return p.state(ctx, request{Operation: "compute", Reference: r, Compute: &c})
}
func (p *checkpointProvider) Suspend(ctx context.Context, q sandbox.SuspendRequest) (sandbox.ComputeState, error) {
	return p.state(ctx, request{Operation: "suspend", Reference: q.Reference, Suspend: &q})
}
func (p *checkpointProvider) Resume(ctx context.Context, q sandbox.ResumeRequest) (sandbox.ComputeState, error) {
	return p.state(ctx, request{Operation: "resume", Reference: q.Reference, Resume: &q})
}
func (p *checkpointProvider) KillCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) error {
	_, e := p.call(ctx, request{Operation: "kill_compute", Reference: r, Compute: &c})
	return e
}
func (p *checkpointProvider) DeleteSnapshot(ctx context.Context, r sandbox.Reference, s sandbox.SnapshotIdentity) error {
	_, e := p.call(ctx, request{Operation: "delete_snapshot", Reference: r, Snapshot: &s})
	return e
}
func (p *checkpointProvider) RunCommandCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute, v sandbox.Command) (sandbox.CommandResult, error) {
	return p.command(ctx, request{Operation: "command_compute", Reference: r, Compute: &c, Command: &v})
}
func (p *checkpointProvider) ResumeCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	return p.state(ctx, request{Operation: "resume_compute", Reference: r, Compute: &c})
}
