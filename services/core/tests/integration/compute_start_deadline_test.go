package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// failedComputeProvider settles Create but its compute is natively failed, so
// it never reaches running with a completed bootstrap.
type failedComputeProvider struct{ lifecycleProvider }

func (p *failedComputeProvider) Create(_ context.Context, b sandbox.Bootstrap) (sandbox.Info, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.creates++
	info := sandbox.Info{Reference: b.Reference, ProviderID: b.AllocationID, State: "failed", CreateSettled: true}
	p.resources[b.AllocationID] = info
	return info, nil
}

func backdateAllocations(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET created_at = created_at - interval '11 minutes'"); err != nil {
		t.Fatal(err)
	}
}

func TestComputeThatNeverStartsFailsStandaloneEnvironmentAtDeadline(t *testing.T) {
	s, key := configuredStore(t)
	p := &failedComputeProvider{lifecycleProvider{resources: map[string]sandbox.Info{}}}
	w, _ := managedWorker(t, s, key, p)
	request := publicPoster(t, s, uuid.NewString(), "start-deadline")
	environment := createStandalone(t, request)
	reconcile := func() {
		for range 4 {
			if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
	}
	reconcile()
	_, body := request("GET", "/agents/environments/"+environment, "")
	if p.kills != 0 || strings.Contains(body, `"failed"`) {
		t.Fatalf("before the deadline: kills=%d environment=%s", p.kills, body)
	}
	backdateAllocations(t, s)
	p.unavailable = true // The deadline holds whatever the Provider reports.
	reconcile()
	_, body = request("GET", "/agents/environments/"+environment, "")
	var state string
	if err := s.pool.QueryRow(t.Context(), "SELECT state FROM runtime_allocations WHERE environment_id=$1", environment).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if p.kills == 0 || state != "released" || !strings.Contains(body, `"failed"`) {
		t.Fatalf("after the deadline: kills=%d allocation=%s environment=%s", p.kills, state, body)
	}
}

func TestRunningAllocationIsNeverFailedByStartDeadline(t *testing.T) {
	s, key := configuredStore(t)
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	w, _ := managedWorker(t, s, key, p)
	request := publicPoster(t, s, uuid.NewString(), "start-deadline")
	environment := createStandalone(t, request)
	for range 3 {
		if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	var state string
	if err := s.pool.QueryRow(t.Context(), "SELECT state FROM runtime_allocations WHERE environment_id=$1", environment).Scan(&state); err != nil || state != "running" {
		t.Fatal("allocation not running", state, err)
	}
	backdateAllocations(t, s)
	p.mu.Lock()
	for id, info := range p.resources {
		info.State = "stopped"
		p.resources[id] = info
	}
	p.mu.Unlock()
	for range 3 {
		if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	_, body := request("GET", "/agents/environments/"+environment, "")
	if p.kills != 0 || strings.Contains(body, `"failed"`) {
		t.Fatalf("running allocation was failed: kills=%d environment=%s", p.kills, body)
	}
}
