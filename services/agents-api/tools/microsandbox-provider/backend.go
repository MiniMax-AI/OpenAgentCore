//go:build linux

package main

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	wire "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

const bootstrapLabel = "io.parsar.bootstrap"

type backend struct{ q wire.Request }

func (b backend) run(ctx context.Context) (wire.Response, error) {
	switch b.q.Operation {
	case "create":
		s, e := b.create(ctx)
		return wire.Response{State: &s}, e
	case "inspect":
		_, s, e := b.inspect(ctx, b.q.Compute)
		return wire.Response{State: &s}, e
	case "metrics":
		m, e := b.metrics(ctx, b.q.Compute)
		return wire.Response{Metrics: m}, e
	case "kill":
		return wire.Response{}, b.kill(ctx, b.q.Compute)
	case "resume_compute":
		s, e := b.resumeCompute(ctx, b.q.Compute)
		return wire.Response{State: &s}, e
	case "command":
		h, _, e := b.inspect(ctx, b.q.Compute)
		if e != nil {
			return wire.Response{}, e
		}
		live, e := h.Connect(ctx)
		if e != nil {
			return wire.Response{}, e
		}
		defer live.Detach(context.Background())
		commandCtx, cancel := context.WithDeadline(ctx, b.q.Deadline)
		defer cancel()
		result, e := runCommand(commandCtx, live, *b.q.Command, "1000:1000")
		return wire.Response{Command: &result}, e
	case "suspend":
		s, e := b.suspend(ctx, *b.q.Suspend)
		return wire.Response{State: &s}, e
	case "resume":
		s, e := b.resume(ctx, *b.q.Resume)
		return wire.Response{State: &s}, e
	case "delete_snapshot":
		_, e := b.verifiedSnapshot(ctx, *b.q.Snapshot)
		if sdk.IsKind(e, sdk.ErrSnapshotNotFound) {
			return wire.Response{}, nil
		}
		if e != nil {
			return wire.Response{}, e
		}
		return wire.Response{}, sdk.Snapshot.Remove(ctx, b.q.Snapshot.Reference, false)
	}
	return wire.Response{}, sandbox.ErrInvalid
}

func (b backend) inspect(ctx context.Context, c wire.Compute) (*sdk.SandboxHandle, wire.State, error) {
	state := wire.State{Compute: c}
	h, e := sdk.GetSandbox(ctx, c.Name)
	if e != nil {
		return nil, state, e
	}
	state, e = qualifyCompute(b.q.Config, b.q.Reference, c, h.ID(), string(h.Status()), h.ConfigJSON())
	return h, state, e
}
func (b backend) kill(ctx context.Context, c wire.Compute) error {
	h, _, e := b.inspect(ctx, c)
	if sdk.IsKind(e, sdk.ErrSandboxNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	if e = h.Kill(ctx); e != nil {
		return e
	}
	if e = h.Remove(ctx); e != nil {
		return e
	}
	_, e = sdk.GetSandbox(ctx, c.Name)
	if sdk.IsKind(e, sdk.ErrSandboxNotFound) {
		return nil
	}
	if e == nil {
		return errors.New("compute removal unconfirmed")
	}
	return e
}
func (b backend) network() *sdk.NetworkConfig {
	n := b.q.Config.Network
	result := &sdk.NetworkConfig{DefaultEgress: sdk.PolicyAction(n.DefaultEgress), DefaultIngress: sdk.PolicyAction(n.DefaultIngress)}
	for _, r := range n.Rules {
		result.Rules = append(result.Rules, sdk.PolicyRule{Action: sdk.PolicyAction(r.Action), Direction: sdk.PolicyDirection(r.Direction), Destination: r.Destination, Protocol: sdk.PolicyProtocol(r.Protocol), Port: r.Port})
	}
	return result
}

func (b backend) resumeCompute(ctx context.Context, c wire.Compute) (wire.State, error) {
	h, state, e := b.inspect(ctx, c)
	if e != nil {
		return state, e
	}
	if h.Status() == sdk.SandboxStatusRunning {
		return state, nil
	}
	if h.Status() != sdk.SandboxStatusPaused {
		return state, wire.ErrUnconfirmed
	}
	// The allocation flock excludes every managed replacement. The pinned Go
	// handle Resume is name-based, so check identity before and after it.
	if e = h.Resume(ctx); e != nil {
		return state, e
	}
	if _, e = h.Refresh(ctx); e != nil {
		return state, e
	}
	_, state, e = b.inspect(ctx, c)
	if e == nil && state.Status != "running" {
		e = wire.ErrUnconfirmed
	}
	return state, e
}
