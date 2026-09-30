package store_test

import (
	"context"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

type configurationCleanupProvider struct {
	lifecycleProvider
	rejectCreate, settleCreate bool
	settleInspection, foreign  bool
	inspectionError, killError error
}

func (p *configurationCleanupProvider) Create(ctx context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	info, err := p.lifecycleProvider.Create(ctx, b)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rejectCreate {
		info = p.resources[b.AllocationID]
		info.State, info.BootstrapComplete, info.CreateSettled = "created", false, p.settleCreate
		p.resources[b.AllocationID] = info
		return info, sandbox.ErrInvalid
	}
	return info, err
}

func (p *configurationCleanupProvider) GetInfo(ctx context.Context, ref sandbox.Reference) (sandbox.Info, error) {
	info, err := p.lifecycleProvider.GetInfo(ctx, ref)
	if err != nil {
		return info, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inspectionError == nil {
		return info, nil
	}
	if !p.settleInspection {
		return sandbox.Info{}, p.inspectionError
	}
	info.State, info.BootstrapComplete, info.CreateSettled = "created", false, true
	if p.foreign {
		info.AllocationID = uuid.NewString()
	}
	return info, p.inspectionError
}

func (p *configurationCleanupProvider) Kill(ctx context.Context, ref sandbox.Reference) error {
	p.mu.Lock()
	if p.killError != nil {
		p.kills++
		err := p.killError
		p.mu.Unlock()
		return err
	}
	p.mu.Unlock()
	return p.lifecycleProvider.Kill(ctx, ref)
}

func TestManagedRuntimeConfigurationCleanup(t *testing.T) {
	for _, test := range []struct {
		name                       string
		rejectCreate, settleCreate bool
		loseCreate                 bool
		settleInspection, foreign  bool
		inspectionError, killError error
		wantReleased, wantSettled  bool
		wantKill                   bool
	}{
		{name: "initial rejection", rejectCreate: true, settleCreate: true, inspectionError: sandbox.ErrInvalid, wantReleased: true, wantSettled: true, wantKill: true},
		{name: "later drift", inspectionError: sandbox.ErrInvalid, wantReleased: true, wantSettled: true, wantKill: true},
		{name: "observed rejection settles lost response", loseCreate: true, settleInspection: true, inspectionError: sandbox.ErrInvalid, wantReleased: true, wantSettled: true, wantKill: true},
		{name: "unknown creation stays retained", loseCreate: true, inspectionError: sandbox.ErrInvalid, wantKill: true},
		{name: "foreign settlement rejected", loseCreate: true, settleInspection: true, foreign: true, inspectionError: sandbox.ErrInvalid},
		{name: "foreign ownership rejected", loseCreate: true, inspectionError: sandbox.ErrOwnership},
		{name: "unavailable observation stays retained", loseCreate: true, inspectionError: sandbox.ErrComputeUnconfirmed},
		{name: "kill ownership rejection stays retained", inspectionError: sandbox.ErrInvalid, killError: sandbox.ErrOwnership, wantSettled: true, wantKill: true},
		{name: "kill unavailable stays retained", inspectionError: sandbox.ErrInvalid, killError: sandbox.ErrComputeUnconfirmed, wantSettled: true, wantKill: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, db := newManagedTestStoreDB(t)
			key := uuid.NewString()
			p := &configurationCleanupProvider{
				lifecycleProvider: lifecycleProvider{resources: map[string]sandbox.Info{}, loseCreate: test.loseCreate},
				rejectCreate:      test.rejectCreate, settleCreate: test.settleCreate,
				settleInspection: test.settleInspection, foreign: test.foreign,
				inspectionError: test.inspectionError, killError: test.killError,
			}
			w, _ := managedWorker(t, s, db, key, p)
			tenant, session, environment := managedSession(t, s, db)
			owner, err := w.ProvisionEnvironment(t.Context(), tenant, environment.ID, key)
			if (err != nil) != (test.rejectCreate || test.loseCreate) || owner.ID == "" {
				t.Fatal("unexpected creation outcome", owner, err)
			}
			if test.rejectCreate {
				value, err := s.GetSession(t.Context(), tenant, session.ID)
				if err != nil || value.Environment.Status != "failed" {
					t.Fatal("rejected configuration did not terminate its retained Session", value, err)
				}
			} else {
				// Configuration drift alone cannot authorize deletion, even when an
				// inspection supplies settlement evidence for the original attempt.
				if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
					t.Fatal(err)
				}
				before, err := s.GetRuntimeAllocation(t.Context(), tenant, environment.ID)
				if err != nil || before.State == "cleanup_pending" || before.State == "released" {
					t.Fatal("drift authorized cleanup", before, err)
				}
				p.mu.Lock()
				kills := p.kills
				p.mu.Unlock()
				if kills != 0 {
					t.Fatal("drift deleted live allocation")
				}
				// Exercise the existing authorized deletion lifecycle; no new
				// deployment cleanup API is introduced by this regression test.
				if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
					t.Fatal(err)
				}
			}

			wantState := "cleanup_pending"
			if test.wantReleased {
				wantState = "released"
			}
			reconcileManagedState(t, w, s, tenant, environment.ID, wantState)
			// A second observation must not turn absence after Kill into proof
			// that an unknown original Create can no longer mutate resources.
			if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetRuntimeAllocation(t.Context(), tenant, environment.ID)
			if err != nil || got.ID != owner.ID || got.State != wantState || got.CreateSettled != test.wantSettled {
				t.Fatal("cleanup lost ownership or settlement", got, err)
			}
			if _, ok, err := fixtureSessionStore(db).GetDeviceCredential(t.Context(), owner.DeviceID); err != nil || ok {
				t.Fatal("cleanup retained execution authority", err)
			}
			p.mu.Lock()
			creates, kills, remaining := p.creates, p.kills, len(p.resources)
			p.mu.Unlock()
			if creates != 1 || (kills > 0) != test.wantKill {
				t.Fatal("creation replayed or cleanup bypassed", creates, kills)
			}
			if test.wantReleased && remaining != 0 || test.killError != nil && remaining == 0 {
				t.Fatal("cleanup contradicted native resource result", remaining)
			}
			if test.rejectCreate {
				// The rejected Session remains publicly readable after cleanup.
				if _, err := s.GetSession(t.Context(), tenant, session.ID); err != nil {
					t.Fatal("configuration rejection deleted Session history", err)
				}
			}
		})
	}
}
