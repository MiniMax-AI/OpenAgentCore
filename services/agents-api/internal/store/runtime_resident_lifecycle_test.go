package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

type fakeResidentProvider struct {
	*fakeCheckpointProvider
	pauses, resumes int
	losePause       bool
}

func (p *fakeResidentProvider) Create(ctx context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	info, err := p.fakeCheckpointProvider.Create(ctx, b)
	if err != nil {
		return info, err
	}
	p.mu.Lock()
	info.CreateSettled = true
	p.resources[b.AllocationID] = info
	p.mu.Unlock()
	return info, nil
}
func (p *fakeResidentProvider) Pause(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	info, err := p.GetInfo(ctx, r)
	if err != nil {
		return info, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if info.State != "running" {
		return info, errors.New("resident pause replayed")
	}
	p.pauses++
	info.State = "paused"
	p.resources[r.AllocationID] = info
	if p.losePause {
		p.losePause = false
		return sandbox.Info{}, sandbox.ErrComputeUnconfirmed
	}
	return info, nil
}
func (p *fakeResidentProvider) Resume(ctx context.Context, r sandbox.Reference) (sandbox.Info, error) {
	info, err := p.GetInfo(ctx, r)
	if err != nil {
		return info, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if info.State != "paused" {
		return info, errors.New("resident resume replayed")
	}
	p.resumes++
	info.State = "running"
	p.resources[r.AllocationID] = info
	return info, nil
}
func (p *fakeResidentProvider) RunCommand(ctx context.Context, r sandbox.Reference, command sandbox.Command) (sandbox.CommandResult, error) {
	if len(command.Args) != 8 || command.Args[0] != "oac-daemon" || command.Args[1] != "resume" || command.Args[5] != r.EnvironmentID {
		return sandbox.CommandResult{}, errors.New("unexpected resident wake command")
	}
	p.mu.Lock()
	p.wakeCommands++
	b := p.bootstraps[r.AllocationID]
	p.mu.Unlock()
	return sandbox.CommandResult{}, p.connect(ctx, b)
}

func TestResidentIdlePauseAndWakeKeepOriginalCompute(t *testing.T) {
	var resident *fakeResidentProvider
	f := newComputeLifecycleFixtureWithProvider(t, 2, 4, func(p *fakeCheckpointProvider) sandbox.SandboxProvider {
		resident = &fakeResidentProvider{fakeCheckpointProvider: p}
		return resident
	})
	tenant, _, environment, owner := f.create()
	var before struct{ Current sandbox.Compute }
	if err := json.Unmarshal(owner.ComputeState, &before); err != nil {
		t.Fatal(err)
	}
	f.complete(owner)
	owner = f.phase(tenant, environment.ID, "suspended")
	if resident.pauses != 1 || resident.resumes != 0 || f.provider.computeKills != 0 || f.provider.captures != 0 {
		t.Fatal("resident pause created or destroyed compute", resident.pauses, f.provider.computeKills)
	}
	f.queued(owner)
	owner = f.phase(tenant, environment.ID, "running")
	var after struct{ Current sandbox.Compute }
	if err := json.Unmarshal(owner.ComputeState, &after); err != nil {
		t.Fatal(err)
	}
	if before.Current.ID == "" || before.Current.ID != after.Current.ID || resident.resumes != 1 || f.provider.wakeCommands != 1 {
		t.Fatal("original compute or daemon connection was not restored")
	}
}

func TestResidentUnknownPauseUsesObservationWithoutReplay(t *testing.T) {
	var resident *fakeResidentProvider
	f := newComputeLifecycleFixtureWithProvider(t, 2, 4, func(p *fakeCheckpointProvider) sandbox.SandboxProvider {
		resident = &fakeResidentProvider{fakeCheckpointProvider: p, losePause: true}
		return resident
	})
	tenant, _, environment, owner := f.create()
	f.phase(tenant, environment.ID, "running")
	f.complete(owner)
	if err := f.worker.ReconcileManagedRuntimes(t.Context()); !errors.Is(err, sandbox.ErrComputeUnconfirmed) {
		t.Fatal("unknown pause was reported as settled", err)
	}
	f.phase(tenant, environment.ID, "suspended")
	if resident.pauses != 1 {
		t.Fatal("unknown pause was replayed", resident.pauses)
	}
}

func TestResidentDoesNotPauseWhileTurnQueued(t *testing.T) {
	var resident *fakeResidentProvider
	f := newComputeLifecycleFixtureWithProvider(t, 2, 4, func(p *fakeCheckpointProvider) sandbox.SandboxProvider {
		resident = &fakeResidentProvider{fakeCheckpointProvider: p}
		return resident
	})
	tenant, _, environment, owner := f.create()
	f.complete(owner)
	f.queued(owner)
	f.phase(tenant, environment.ID, "running")
	if resident.pauses != 0 {
		t.Fatal("busy session was paused")
	}
}

func TestResidentNeverUsedSessionPausesAfterInitializationIdle(t *testing.T) {
	var resident *fakeResidentProvider
	f := newComputeLifecycleFixtureWithProvider(t, 2, 4, func(p *fakeCheckpointProvider) sandbox.SandboxProvider {
		resident = &fakeResidentProvider{fakeCheckpointProvider: p}
		return resident
	})
	tenant, _, environment, owner := f.create()
	// The initial Runtime-ready touch prevents an immediate pause even when
	// provisioning took longer than the idle threshold.
	f.phase(tenant, environment.ID, "running")
	f.sql(`UPDATE runtime_allocations SET compute_activity_at=clock_timestamp()-interval '10 minutes',created_at=clock_timestamp()-interval '10 minutes' WHERE id=$1`, owner.ID)
	f.phase(tenant, environment.ID, "suspended")
	if resident.pauses != 1 {
		t.Fatal("never-used idle Session kept a running sandbox")
	}
}
