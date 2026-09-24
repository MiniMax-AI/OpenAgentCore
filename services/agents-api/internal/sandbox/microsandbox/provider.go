package microsandbox

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

var ErrUnconfirmed = sandbox.ErrComputeUnconfirmed

type Provider struct {
	config Config
	caller Caller
}

var _ sandbox.Provider = (*Provider)(nil)
var _ sandbox.CheckpointProvider = (*Provider)(nil)

func New(c Config) (*Provider, error) { return NewWithCaller(c, &ProcessCaller{}) }
func NewWithCaller(c Config, caller Caller) (*Provider, error) {
	if c.Validate() != nil || caller == nil {
		return nil, sandbox.ErrInvalid
	}
	c.Network.Rules = append([]NetworkRule(nil), c.Network.Rules...)
	return &Provider{config: c, caller: caller}, nil
}
func (p *Provider) Initial(ctx context.Context, r sandbox.Reference) (Compute, error) {
	if err := ctx.Err(); err != nil {
		return Compute{}, err
	}
	if !ValidReference(r) {
		return Compute{}, sandbox.ErrInvalid
	}
	return Compute{Name: Name(p.config, r, 0)}, nil
}
func (p *Provider) call(ctx context.Context, q Request) (Response, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return Response{}, sandbox.ErrInvalid
	}
	q.Version, q.Config, q.Deadline = ProtocolVersion, p.config, deadline
	if err := ValidateRequest(q); err != nil {
		return Response{}, err
	}
	out, err := p.caller.Call(ctx, q)
	if err != nil {
		if q.Operation == "command" {
			return out, errors.Join(sandbox.ErrCommandUnconfirmed, err)
		}
		return out, errors.Join(ErrUnconfirmed, err)
	}
	if out.Version != ProtocolVersion {
		return out, ErrUnconfirmed
	}
	switch out.ErrorCode {
	case "":
		return out, nil
	case "invalid":
		return out, sandbox.ErrInvalid
	case "ownership":
		return out, sandbox.ErrOwnership
	case "exists":
		return out, sandbox.ErrExists
	case "not_found":
		return out, sandbox.ErrNotFound
	case "command_unconfirmed":
		return out, sandbox.ErrCommandUnconfirmed
	case "metrics_unavailable":
		return out, runtimeobs.ErrUnavailable
	default:
		return out, ErrUnconfirmed
	}
}
func (p *Provider) state(ctx context.Context, q Request) (State, error) {
	out, e := p.call(ctx, q)
	if e != nil {
		return State{}, e
	}
	return p.responseState(ctx, q, out)
}

func (p *Provider) responseState(ctx context.Context, q Request, out Response) (State, error) {
	var e error
	if out.State == nil || ValidateCompute(p.config, q.Reference, out.State.Compute) != nil || out.State.Compute.ID == "" {
		return State{}, ErrUnconfirmed
	}
	want := q.Compute
	if q.Operation == "create" {
		want, e = p.Initial(ctx, q.Reference)
		if e != nil {
			return State{}, e
		}
	}
	if q.Operation == "suspend" {
		want = q.Suspend.Source
	}
	if q.Operation == "resume" {
		want = q.Resume.Target
	}
	got := out.State.Compute
	if (got.RestoredFrom == nil) != (want.RestoredFrom == nil) || (want.RestoredFrom != nil && *got.RestoredFrom != *want.RestoredFrom) {
		return State{}, sandbox.ErrOwnership
	}
	if got.Name != want.Name || got.Generation != want.Generation || (want.ID != "" && got.ID != want.ID) {
		return State{}, sandbox.ErrOwnership
	}
	if q.Operation == "suspend" {
		s := out.State.Snapshot
		if s == nil && q.Suspend.ObserveOnly && out.State.BootstrapComplete && !out.State.SourceStopped && (out.State.Status == "running" || out.State.Status == "paused") {
			return *out.State, nil
		}
		if s == nil || ValidateSnapshot(p.config, q.Reference, *s) != nil || s.OperationID != q.Suspend.OperationID || s.SourceID != want.ID || (!q.Suspend.ObserveOnly && (!out.State.SourceStopped || out.State.Status != "suspended")) {
			return State{}, ErrUnconfirmed
		}
	}
	return *out.State, nil
}
func info(r sandbox.Reference, s State) sandbox.Info {
	return sandbox.Info{Reference: r, ProviderID: s.Compute.ID, State: s.Status, BootstrapComplete: s.BootstrapComplete}
}
func (p *Provider) Create(ctx context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	q := Request{Operation: "create", Reference: b.Reference, Bootstrap: &b}
	out, err := p.call(ctx, q)
	settledRejection := errors.Is(err, sandbox.ErrInvalid) && !errors.Is(err, ErrUnconfirmed) && out.CreateSettled
	if err != nil && !settledRejection {
		return sandbox.Info{Reference: b.Reference}, err
	}
	s, stateErr := p.responseState(ctx, q, out)
	if stateErr != nil {
		return sandbox.Info{Reference: b.Reference}, stateErr
	}
	if settledRejection && (s.Status == "" || s.Status == "absent" || s.BootstrapComplete) {
		return sandbox.Info{Reference: b.Reference}, ErrUnconfirmed
	}
	result := info(b.Reference, s)
	if settledRejection {
		// Settlement is independent of readiness and survives the original error.
		result.CreateSettled, result.BootstrapComplete = true, false
	}
	return result, err
}
func (p *Provider) GetInfo(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	c, e := p.Initial(ctx, r)
	if e != nil {
		return sandbox.Info{}, e
	}
	s, e := p.GetCompute(ctx, r, c)
	return info(r, s), e
}
func (p *Provider) Renew(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	return p.GetInfo(ctx, r)
}
func (p *Provider) Kill(ctx context.Context, r sandbox.Reference) error {
	c, e := p.Initial(ctx, r)
	if e != nil {
		return e
	}
	return p.KillCompute(ctx, r, c)
}
func (p *Provider) RunCommand(ctx context.Context, r sandbox.Reference, c sandbox.Command) (sandbox.CommandResult, error) {
	compute, e := p.Initial(ctx, r)
	if e != nil {
		return sandbox.CommandResult{}, e
	}
	return p.RunCommandCompute(ctx, r, compute, c)
}
func (p *Provider) GetCompute(ctx context.Context, r sandbox.Reference, c Compute) (State, error) {
	return p.state(ctx, Request{Operation: "inspect", Reference: r, Compute: c})
}
func (p *Provider) KillCompute(ctx context.Context, r sandbox.Reference, c Compute) error {
	_, e := p.call(ctx, Request{Operation: "kill", Reference: r, Compute: c})
	return e
}
func (p *Provider) DeleteSnapshot(ctx context.Context, r sandbox.Reference, s SnapshotIdentity) error {
	_, e := p.call(ctx, Request{Operation: "delete_snapshot", Reference: r, Snapshot: &s})
	return e
}
func (p *Provider) Suspend(ctx context.Context, q SuspendRequest) (State, error) {
	return p.state(ctx, Request{Operation: "suspend", Reference: q.Reference, Suspend: &q})
}
func (p *Provider) Resume(ctx context.Context, q ResumeRequest) (State, error) {
	return p.state(ctx, Request{Operation: "resume", Reference: q.Reference, Resume: &q})
}
func (p *Provider) RunCommandCompute(ctx context.Context, r sandbox.Reference, c Compute, command sandbox.Command) (sandbox.CommandResult, error) {
	out, e := p.call(ctx, Request{Operation: "command", Reference: r, Compute: c, Command: &command})
	if e != nil {
		return sandbox.CommandResult{}, e
	}
	if out.Command == nil || len(out.Command.Stdout) > MaxOutputBytes || len(out.Command.Stderr) > MaxOutputBytes {
		return sandbox.CommandResult{}, sandbox.ErrCommandUnconfirmed
	}
	return *out.Command, nil
}

// ResumeCompute thaws the exact resident source after an aborted suspension.
// It never starts stopped compute or restores a checkpoint.
func (p *Provider) ResumeCompute(ctx context.Context, r sandbox.Reference, c Compute) (State, error) {
	return p.state(ctx, Request{Operation: "resume_compute", Reference: r, Compute: c})
}

func (p *Provider) NewCompute(ctx context.Context, r sandbox.Reference, generation uint64, snapshot *SnapshotIdentity) (Compute, error) {
	if err := ctx.Err(); err != nil {
		return Compute{}, err
	}
	c := Compute{Generation: generation, Name: Name(p.config, r, generation)}
	if snapshot != nil {
		value := *snapshot
		c.RestoredFrom = &value
	}
	if e := ValidateCompute(p.config, r, c); e != nil {
		return Compute{}, e
	}
	return c, nil
}
