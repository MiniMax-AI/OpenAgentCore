package microsandbox

import (
	"context"
	"encoding/json"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// retained translates a verified native snapshot without exposing its schema to Core.
func retained(s SnapshotIdentity) sandbox.RetainedState {
	raw, _ := json.Marshal(s)
	return sandbox.RetainedState{Reference: s.Reference, ID: s.ID, OperationID: s.OperationID, SourceGeneration: s.SourceGeneration, SourceName: s.SourceName, SourceID: s.SourceID, Data: string(raw)}
}
func (p *Provider) snapshot(r sandbox.Reference, s sandbox.RetainedState) (SnapshotIdentity, error) {
	var native SnapshotIdentity
	if sandbox.ValidateRetained(s) != nil || json.Unmarshal([]byte(s.Data), &native) != nil || retained(native) != s || ValidateSnapshot(p.config, r, native) != nil {
		return native, sandbox.ErrOwnership
	}
	return native, nil
}
func compute(c Compute) sandbox.Compute {
	out := sandbox.Compute{Generation: c.Generation, Name: c.Name, ID: c.ID}
	if c.RestoredFrom != nil {
		s := retained(*c.RestoredFrom)
		out.RestoredFrom = &s
	}
	return out
}
func (p *Provider) nativeCompute(r sandbox.Reference, c sandbox.Compute) (Compute, error) {
	out := Compute{Generation: c.Generation, Name: c.Name, ID: c.ID}
	if c.RestoredFrom != nil {
		s, e := p.snapshot(r, *c.RestoredFrom)
		if e != nil {
			return out, e
		}
		out.RestoredFrom = &s
	}
	if ValidateCompute(p.config, r, out) != nil {
		return out, sandbox.ErrOwnership
	}
	return out, nil
}
func state(s State) sandbox.ComputeState {
	out := sandbox.ComputeState{Compute: compute(s.Compute), Status: s.Status, BootstrapComplete: s.BootstrapComplete, ResourcesReleased: s.SourceStopped}
	if s.Snapshot != nil {
		v := retained(*s.Snapshot)
		out.Retained = &v
	}
	return out
}
func (p *Provider) Initial(ctx context.Context, r sandbox.Reference) (sandbox.Compute, error) {
	c, e := p.nativeInitial(ctx, r)
	return compute(c), e
}
func (p *Provider) NewCompute(ctx context.Context, r sandbox.Reference, g uint64, s *sandbox.RetainedState) (sandbox.Compute, error) {
	var snap *SnapshotIdentity
	if s != nil {
		v, e := p.snapshot(r, *s)
		if e != nil {
			return sandbox.Compute{}, e
		}
		snap = &v
	}
	c, e := p.nativeNewCompute(ctx, r, g, snap)
	return compute(c), e
}
func (p *Provider) GetCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	n, e := p.nativeCompute(r, c)
	if e != nil {
		return sandbox.ComputeState{}, e
	}
	s, e := p.nativeGetCompute(ctx, r, n)
	return state(s), e
}
func (p *Provider) RenewCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	return p.GetCompute(ctx, r, c)
}
func (p *Provider) Suspend(ctx context.Context, q sandbox.SuspendRequest) (sandbox.ComputeState, error) {
	c, e := p.nativeCompute(q.Reference, q.Source)
	if e != nil {
		return sandbox.ComputeState{}, e
	}
	n := SuspendRequest{Reference: q.Reference, OperationID: q.OperationID, Source: c, ObserveOnly: q.ReconcileOnly}
	if q.Retained != nil {
		s, e := p.snapshot(q.Reference, *q.Retained)
		if e != nil {
			return sandbox.ComputeState{}, e
		}
		n.Snapshot = &s
	}
	s, e := p.nativeSuspend(ctx, n)
	if e == nil && s.Snapshot != nil {
		// Complete the same exact-source cleanup previously performed by Core.
		// Native KillCompute still verifies ownership and removes its disks.
		e = p.nativeKillCompute(ctx, q.Reference, c)
		if e == nil {
			s.SourceStopped = true
			s.Status = "suspended"
		}
	}
	out := state(s)
	// The helper holds the allocation lock until the original native work settles.
	out.SuspendSettled = e == nil
	return out, e
}
func (p *Provider) Resume(ctx context.Context, q sandbox.ResumeRequest) (sandbox.ComputeState, error) {
	c, e := p.nativeCompute(q.Reference, q.Target)
	if e != nil {
		return sandbox.ComputeState{}, e
	}
	s, e := p.snapshot(q.Reference, q.Retained)
	if e != nil {
		return sandbox.ComputeState{}, e
	}
	v, e := p.nativeResume(ctx, ResumeRequest{Reference: q.Reference, OperationID: q.OperationID, Snapshot: s, Target: c, ObserveOnly: q.ReconcileOnly})
	return state(v), e
}
func (p *Provider) KillCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) error {
	n, e := p.nativeCompute(r, c)
	if e != nil {
		return e
	}
	return p.nativeKillCompute(ctx, r, n)
}
func (p *Provider) DeleteRetained(ctx context.Context, r sandbox.Reference, s sandbox.RetainedState) error {
	n, e := p.snapshot(r, s)
	if e != nil {
		return e
	}
	return p.nativeDeleteSnapshot(ctx, r, n)
}
func (p *Provider) RunCommandCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute, q sandbox.Command) (sandbox.CommandResult, error) {
	n, e := p.nativeCompute(r, c)
	if e != nil {
		return sandbox.CommandResult{}, e
	}
	return p.nativeRunCommandCompute(ctx, r, n, q)
}
func (p *Provider) ResumeCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	n, e := p.nativeCompute(r, c)
	if e != nil {
		return sandbox.ComputeState{}, e
	}
	v, e := p.nativeResumeCompute(ctx, r, n)
	return state(v), e
}
