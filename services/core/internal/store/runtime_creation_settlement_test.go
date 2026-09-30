package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

type absentCreationProvider struct {
	lifecycleProvider
	cancel         context.CancelFunc
	foreign        bool
	observeSettled bool
}

func (p *absentCreationProvider) Create(_ context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	p.creates++
	if p.cancel != nil {
		p.cancel()
	}
	if p.observeSettled {
		return sandbox.Info{}, errors.New("fixture: uncertain response")
	}
	ref := b.Reference
	if p.foreign {
		ref.AllocationID = uuid.NewString()
	}
	return sandbox.Info{Reference: ref, State: "absent", CreateSettled: true}, sandbox.ErrInvalid
}
func (p *absentCreationProvider) GetInfo(_ context.Context, r sandbox.Reference) (sandbox.Info, error) {
	if p.observeSettled {
		return sandbox.Info{Reference: r, State: "absent", CreateSettled: true}, nil
	}
	return sandbox.Info{}, sandbox.ErrNotFound
}
func TestManagedRuntimeConfirmedAbsentCreateReleasesAtomically(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "live caller", true: "cancelled caller"}[cancelled], func(t *testing.T) {
			s, _ := store.NewManagedTestStore(t)
			key := uuid.NewString()
			p := &absentCreationProvider{}
			w, _ := managedWorker(t, s, key, p)
			tenant, session, environment := managedSession(t, s)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cancelled {
				p.cancel = cancel
			}
			owner, err := w.ProvisionEnvironment(ctx, tenant, environment.ID, key)
			if err == nil || owner.State != "released" || !owner.CreateSettled {
				t.Fatal("confirmed absence not atomically released", owner, err)
			}
			if p.kills != 0 || p.creates != 1 {
				t.Fatal("absence proof still called external cleanup", p.kills, p.creates)
			}
			stored, err := s.GetRuntimeAllocation(t.Context(), tenant, environment.ID)
			if err != nil || stored.State != "released" || !stored.CreateSettled {
				t.Fatal("release not durable", err)
			}
			if _, ok, err := s.GetDeviceCredential(t.Context(), owner.DeviceID); err != nil || ok {
				t.Fatal("released credential retained authority", err)
			}
			value, err := s.GetSession(t.Context(), tenant, session.ID)
			if err != nil || value.Environment.Status != "failed" {
				t.Fatal("environment not terminated", err)
			}
			if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
				t.Fatal(err)
			}
			if p.kills != 0 {
				t.Fatal("released absence was cleaned again")
			}
		})
	}
}
func TestManagedRuntimeForeignAbsenceCannotReleaseCreation(t *testing.T) {
	s, _ := store.NewManagedTestStore(t)
	key := uuid.NewString()
	p := &absentCreationProvider{foreign: true}
	w, _ := managedWorker(t, s, key, p)
	tenant, _, environment := managedSession(t, s)
	if _, err := w.ProvisionEnvironment(t.Context(), tenant, environment.ID, key); err == nil {
		t.Fatal("foreign absence accepted")
	}
	owner, err := s.GetRuntimeAllocation(t.Context(), tenant, environment.ID)
	if err != nil || owner.CreateSettled || owner.State == "released" {
		t.Fatal("foreign proof settled original attempt", owner, err)
	}
}
func TestManagedRuntimeObservedSettlementAllowsOwnedCleanup(t *testing.T) {
	s, _ := store.NewManagedTestStore(t)
	key := uuid.NewString()
	p := &absentCreationProvider{observeSettled: true}
	w, _ := managedWorker(t, s, key, p)
	tenant, session, environment := managedSession(t, s)
	if _, err := w.ProvisionEnvironment(t.Context(), tenant, environment.ID, key); err == nil {
		t.Fatal("uncertain Create succeeded")
	}
	if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	reconcileManagedState(t, w, s, tenant, environment.ID, "released")
	if p.kills != 1 || p.creates != 1 {
		t.Fatal("settled observation replayed Create or skipped cleanup", p.creates, p.kills)
	}
}
