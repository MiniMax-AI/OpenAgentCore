package e2b

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// One E2B sandbox spans every generation of an allocation: pause keeps the
// sandbox and resume reconnects to it. The helper's private receipt records the
// current generation, so only that generation addresses the sandbox.

func digest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

// compatibility names the endpoint and installation that own a paused sandbox
// and the template build it runs; a snapshot restores only under the same two.
func (c Config) compatibility() sandbox.CheckpointCompatibility {
	apiURL, domain, _ := NormalizeEndpoint(c.APIURL, c.Domain)
	return sandbox.CheckpointCompatibility{ArtifactDomain: digest("e2b-endpoint", c.InstallationID, apiURL, domain), ExecutionClass: digest("e2b-template", c.Template)}
}

func (p *Provider) name(r sandbox.Reference, generation uint64) string {
	sum := sha256.Sum256([]byte(p.config.InstallationID + ":" + r.TenantID + ":" + r.EnvironmentID + ":" + r.AllocationID))
	return fmt.Sprintf("oac-%x-g%d", sum[:16], generation)
}

func (p *Provider) validSnapshot(r sandbox.Reference, s sandbox.SnapshotIdentity) bool {
	return s.Validate() == nil && validID(s.OperationID) && s.SourceName == p.name(r, s.SourceGeneration) &&
		s.Reference == s.SourceName+":s-"+s.OperationID && s.SourceID != "" && s.ID != ""
}

func (p *Provider) validCompute(r sandbox.Reference, c sandbox.Compute) bool {
	if !validReference(r) || c.Name != p.name(r, c.Generation) {
		return false
	}
	if c.Generation == 0 {
		return c.RestoredFrom == nil
	}
	return c.RestoredFrom != nil && p.validSnapshot(r, *c.RestoredFrom) && c.RestoredFrom.SourceGeneration < c.Generation
}

func (p *Provider) Initial(ctx context.Context, r sandbox.Reference) (sandbox.Compute, error) {
	if err := ctx.Err(); err != nil {
		return sandbox.Compute{}, err
	}
	if !validReference(r) {
		return sandbox.Compute{}, sandbox.ErrInvalid
	}
	return sandbox.Compute{Name: p.name(r, 0)}, nil
}

func (p *Provider) NewCompute(ctx context.Context, r sandbox.Reference, generation uint64, snapshot *sandbox.SnapshotIdentity) (sandbox.Compute, error) {
	if err := ctx.Err(); err != nil {
		return sandbox.Compute{}, err
	}
	c := sandbox.Compute{Generation: generation, Name: p.name(r, generation)}
	if snapshot != nil {
		value := *snapshot
		c.RestoredFrom = &value
	}
	if !p.validCompute(r, c) {
		return sandbox.Compute{}, sandbox.ErrInvalid
	}
	return c, nil
}

// state sends one checkpoint request and validates the returned incarnation
// against the one the request named.
func (p *Provider) state(ctx context.Context, q Request, want sandbox.Compute) (sandbox.ComputeState, error) {
	deadline, ok := ctx.Deadline()
	if !ok || !p.validCompute(q.Reference, want) {
		return sandbox.ComputeState{}, sandbox.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return sandbox.ComputeState{}, err
	}
	q.Version, q.Config, q.Deadline = ProtocolVersion, p.config, deadline
	out, err := p.exchange(ctx, q)
	if err != nil {
		return sandbox.ComputeState{}, err
	}
	s := out.State
	if s == nil {
		return sandbox.ComputeState{}, sandbox.ErrComputeUnconfirmed
	}
	if s.RestoreAttemptClosed != "" {
		if q.Resume == nil || !s.ClosesRestoreAttempt(*q.Resume) {
			return sandbox.ComputeState{}, sandbox.ErrComputeUnconfirmed
		}
		return *s, nil
	}
	got := s.Compute
	if got.ID == "" || got.Name != want.Name || got.Generation != want.Generation || (want.ID != "" && got.ID != want.ID) ||
		(got.RestoredFrom == nil) != (want.RestoredFrom == nil) || want.RestoredFrom != nil && *got.RestoredFrom != *want.RestoredFrom {
		return sandbox.ComputeState{}, sandbox.ErrOwnership
	}
	if q.Suspend != nil {
		snapshot := s.Snapshot
		if snapshot == nil {
			if !q.Suspend.ObserveOnly || s.SourceStopped || !s.BootstrapComplete || s.Status != "running" && s.Status != "paused" {
				return sandbox.ComputeState{}, sandbox.ErrComputeUnconfirmed
			}
		} else if !p.validSnapshot(q.Reference, *snapshot) || snapshot.Compatibility != p.config.Compatibility || snapshot.OperationID != q.Suspend.OperationID ||
			snapshot.SourceID != want.ID && want.ID != "" || snapshot.SourceID != got.ID || !q.Suspend.ObserveOnly && (!s.SourceStopped || s.Status != "paused") {
			return sandbox.ComputeState{}, sandbox.ErrComputeUnconfirmed
		}
	}
	return *s, nil
}

func (p *Provider) GetCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	return p.state(ctx, Request{Operation: "compute", Reference: r, Compute: &c}, c)
}

func (p *Provider) RenewCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	return p.state(ctx, Request{Operation: "renew_compute", Reference: r, Compute: &c}, c)
}

func (p *Provider) ResumeCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	return p.state(ctx, Request{Operation: "resume_compute", Reference: r, Compute: &c}, c)
}

func (p *Provider) Suspend(ctx context.Context, q sandbox.SuspendRequest) (sandbox.ComputeState, error) {
	if !validID(q.OperationID) || q.Snapshot != nil && !p.validSnapshot(q.Reference, *q.Snapshot) {
		return sandbox.ComputeState{}, sandbox.ErrInvalid
	}
	return p.state(ctx, Request{Operation: "suspend", Reference: q.Reference, Suspend: &q}, q.Source)
}

// Resume reconnects to the paused sandbox. A snapshot from another endpoint or
// template build is unusable before any native call.
func (p *Provider) Resume(ctx context.Context, q sandbox.ResumeRequest) (sandbox.ComputeState, error) {
	if q.Workspace != nil {
		return sandbox.ComputeState{}, sandbox.ErrInvalid
	}
	if !validID(q.OperationID) || !p.validSnapshot(q.Reference, q.Snapshot) || q.Target.RestoredFrom == nil || *q.Target.RestoredFrom != q.Snapshot {
		return sandbox.ComputeState{}, sandbox.ErrInvalid
	}
	if q.Snapshot.Compatibility != p.config.Compatibility {
		return sandbox.ComputeState{}, sandbox.ErrCheckpointUnusable
	}
	return p.state(ctx, Request{Operation: "resume", Reference: q.Reference, Resume: &q}, q.Target)
}

func (p *Provider) KillCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute) error {
	deadline, ok := ctx.Deadline()
	if !ok || !p.validCompute(r, c) {
		return sandbox.ErrInvalid
	}
	_, err := p.exchange(ctx, Request{Version: ProtocolVersion, Operation: "kill_compute", Config: p.config, Reference: r, Compute: &c, Deadline: deadline})
	return err
}

func (p *Provider) DeleteSnapshot(ctx context.Context, r sandbox.Reference, s sandbox.SnapshotIdentity) error {
	deadline, ok := ctx.Deadline()
	if !ok || !validReference(r) || !p.validSnapshot(r, s) {
		return sandbox.ErrInvalid
	}
	_, err := p.exchange(ctx, Request{Version: ProtocolVersion, Operation: "delete_snapshot", Config: p.config, Reference: r, Snapshot: &s, Deadline: deadline})
	return err
}

func (p *Provider) RunCommandCompute(ctx context.Context, r sandbox.Reference, c sandbox.Compute, command sandbox.Command) (sandbox.CommandResult, error) {
	if err := validCommand(command); err != nil {
		return sandbox.CommandResult{}, err
	}
	deadline, ok := ctx.Deadline()
	if !ok || !p.validCompute(r, c) {
		return sandbox.CommandResult{}, sandbox.ErrInvalid
	}
	out, err := p.exchange(ctx, Request{Version: ProtocolVersion, Operation: "command_compute", Config: p.config, Reference: r, Compute: &c, Command: &command, Deadline: deadline})
	return commandResult(out, err)
}
