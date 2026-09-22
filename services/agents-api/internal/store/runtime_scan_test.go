package store_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type scanProvider struct {
	lifecycleProvider
	observed []string
}

func (p *scanProvider) GetInfo(ctx context.Context, ref sandbox.Reference) (sandbox.Info, error) {
	p.observed = append(p.observed, ref.AllocationID)
	return p.lifecycleProvider.GetInfo(ctx, ref)
}

func TestManagedRuntimeScanWrapServicesNextPage(t *testing.T) {
	for _, count := range []int{0, 1, 31, 32, 33, 65} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, _ := store.NewManagedTestStore(t)
			p := &scanProvider{lifecycleProvider: lifecycleProvider{resources: map[string]sandbox.Info{}}}
			key := uuid.NewString()
			w, _ := managedWorker(t, s, key, p)
			var ids []string
			for range count {
				tenant, _, env := managedSession(t, s)
				owner, err := w.ProvisionEnvironment(t.Context(), tenant, env.ID, key)
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, owner.ID)
			}
			slices.Sort(ids)
			// Each call must service the next bounded page, including the call
			// that wraps at EOF. Empty stores must return without spinning.
			pages := max(1, (count+31)/32)
			for call := 0; call < pages*3; call++ {
				p.observed = nil
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				err := w.ReconcileManagedRuntimes(ctx)
				cancel()
				if err != nil {
					t.Fatal(err)
				}
				start := (call % pages) * 32
				want := ids[start:min(start+32, count)]
				if !slices.Equal(p.observed, want) {
					t.Fatalf("call %d observed %v, want %v", call, p.observed, want)
				}
			}
			if p.creates != count || p.kills != 0 {
				t.Fatal("scanning changed resource ownership", p.creates, p.kills)
			}
		})
	}
}

func TestManagedRuntimeScanEmptyAfterCleanupAndCanceledCall(t *testing.T) {
	s, _ := store.NewManagedTestStore(t)
	p := &scanProvider{lifecycleProvider: lifecycleProvider{resources: map[string]sandbox.Info{}}}
	key := uuid.NewString()
	w, _ := managedWorker(t, s, key, p)
	tenant, session, env := managedSession(t, s)
	owner, err := w.ProvisionEnvironment(t.Context(), tenant, env.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
		t.Fatal(err)
	}
	p.observed = nil
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := w.ReconcileManagedRuntimes(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled scan", err)
	}
	if len(p.observed) != 0 {
		t.Fatal("canceled scan reached provider")
	}
	if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	// EOF must not delay cleanup to a second call, and cancellation must
	// release the lifecycle gate.
	if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRuntimeAllocation(t.Context(), tenant, env.ID)
	if err != nil || got.State != "released" || p.kills != 1 {
		t.Fatal("cleanup delayed at EOF", got, err, p.kills)
	}
	for range 2 {
		p.observed = nil
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		err := w.ReconcileManagedRuntimes(ctx)
		cancel()
		if err != nil || len(p.observed) != 0 {
			t.Fatal("released allocation reobserved", owner.ID, err, p.observed)
		}
	}
	// A new allocation remains discoverable after the store becomes empty.
	tenant, _, env = managedSession(t, s)
	next, err := w.ProvisionEnvironment(t.Context(), tenant, env.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.observed, []string{next.ID}) {
		t.Fatal("new allocation stranded after empty scan", p.observed)
	}
}
