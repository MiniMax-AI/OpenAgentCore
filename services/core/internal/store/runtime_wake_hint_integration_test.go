package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

type wakeHintScanProvider struct {
	*fakeCheckpointProvider
	sentinel string
	release  chan struct{}
	scans    chan int
	count    atomic.Int32
}

func (p *wakeHintScanProvider) GetCompute(ctx context.Context, reference sandbox.Reference, compute sandbox.Compute) (sandbox.ComputeState, error) {
	if reference.AllocationID == p.sentinel {
		call := int(p.count.Add(1))
		p.scans <- call
		if call == 1 {
			select {
			case <-p.release:
			case <-ctx.Done():
				return sandbox.ComputeState{}, ctx.Err()
			}
		}
	}
	return p.fakeCheckpointProvider.GetCompute(ctx, reference, compute)
}

type wakeHintIntegrationTarget struct {
	tenant      string
	session     sessions.Session
	environment sessions.Environment
	owner       deployment.Allocation
}

type wakeHintIntegration struct {
	fixture  *computeLifecycleFixture
	provider *wakeHintScanProvider
	worker   *execution.Worker
	target   wakeHintIntegrationTarget
	sentinel wakeHintIntegrationTarget
	started  time.Time
	release  func()
}

func newWakeHintIntegration(t *testing.T) *wakeHintIntegration {
	t.Helper()
	f := newComputeLifecycleFixture(t, 2, 4)
	create := func() wakeHintIntegrationTarget {
		tenant, session, environment, owner := f.create()
		return wakeHintIntegrationTarget{tenant, session, environment, owner}
	}
	target, sentinel := create(), create()
	if target.owner.ID > sentinel.owner.ID {
		target, sentinel = sentinel, target
	}
	f.complete(target.owner)
	target.owner = f.phase(target.tenant, target.environment.ID, "suspended")
	f.stop()
	provider := &wakeHintScanProvider{
		fakeCheckpointProvider: f.provider, sentinel: sentinel.owner.ID,
		release: make(chan struct{}), scans: make(chan int, 16),
	}
	worker := startWorker(t, t.Context(), f.db, &execution.Dispatcher{
		Store: f.store, Registry: f.provider.registry,
		ManagedRuntimes: &execution.RuntimeProvider{
			CoreURL: "http://core.invalid/api/v1", InstallationID: f.key,
			BackendFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Provider:           provider, Suspension: &f.policy,
		},
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	var once sync.Once
	release := func() { once.Do(func() { close(provider.release) }) }
	result := &wakeHintIntegration{f, provider, worker, target, sentinel, time.Now(), release}
	go func() { done <- worker.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		release()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Error("maintenance worker stopped unexpectedly", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("maintenance worker did not stop")
		}
	})
	select {
	case call := <-provider.scans:
		if call != 1 {
			t.Fatal("unexpected sentinel scan", call)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("initial scan did not reach the sentinel")
	}
	// UUID ordering places the suspended target before the blocked sentinel.
	// No input exists yet, so the initial scan cannot have restored the target.
	return result
}

func wakeHintInput(text string) []sessions.Input {
	payload, _ := json.Marshal(map[string]string{"text": text})
	return []sessions.Input{{Kind: "message", Payload: payload}}
}

func (f *wakeHintIntegration) pending(t *testing.T, target wakeHintIntegrationTarget, key string) sessions.EnvironmentInputReservation {
	t.Helper()
	var id string
	awaitDaemonRemoteCondition(t, t.Context(), 2*time.Second, "committed wake input", func() bool {
		return f.fixture.db.pool.QueryRow(t.Context(),
			"SELECT id::text FROM environment_input_reservations WHERE session_id=$1 AND idempotency_key=$2",
			target.session.ID, key).Scan(&id) == nil
	})
	pending, err := f.fixture.store.GetEnvironmentInputReservation(t.Context(), target.tenant, target.session.ID, id)
	if err != nil || pending.State != sessions.EnvironmentInputPending {
		t.Fatal("input was not durably pending", pending.State, err)
	}
	return pending
}

func TestRuntimeWakeHintCommittedSubmitResumesBeforeNormalTick(t *testing.T) {
	f := newWakeHintIntegration(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := f.worker.SubmitInputs(ctx, f.target.tenant, f.target.session.ID, "wake", wakeHintInput("next turn"))
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Error("pending input waiter returned unexpectedly", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("pending input waiter did not stop")
		}
	})
	pending := f.pending(t, f.target, "wake")
	// A same-key caller may stop waiting without removing the committed input.
	// Its retry must retain the reservation and cannot restore a second target.
	retryCtx, stopRetry := context.WithTimeout(t.Context(), 250*time.Millisecond)
	_, err := f.worker.SubmitInputs(retryCtx, f.target.tenant, f.target.session.ID, "wake", wakeHintInput("next turn"))
	stopRetry()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("retry did not retain pending admission", err)
	}
	retried := f.pending(t, f.target, "wake")
	if retried.ID != pending.ID || !retried.Deadline.Equal(pending.Deadline) {
		t.Fatal("same-key retry replaced the reservation or its deadline")
	}
	f.release()
	awaitDaemonRemoteCondition(t, t.Context(), 2*time.Second, "hint restored retained compute", func() bool {
		owner, err := fixtureReader(f.fixture.db).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: f.target.tenant, EnvironmentID: f.target.environment.ID})
		return err == nil && owner.ComputePhase == "running"
	})
	if elapsed := time.Since(f.started); elapsed >= 3*time.Second {
		t.Fatalf("restore did not precede the normal five-second tick: %s", elapsed)
	}
	f.provider.mu.Lock()
	restores, creates := f.provider.restores, f.provider.creates
	f.provider.mu.Unlock()
	if restores != 1 || creates != 2 || f.provider.promptFrames.Load() != 0 {
		t.Fatal("wake replayed creation/restoration or sent native input", restores, creates, f.provider.promptFrames.Load())
	}
	var reservations, turns int
	if err := f.fixture.db.pool.QueryRow(t.Context(),
		"SELECT (SELECT count(*) FROM environment_input_reservations WHERE session_id=$1), (SELECT count(*) FROM turns WHERE session_id=$1)",
		f.target.session.ID).Scan(&reservations, &turns); err != nil || reservations != 1 || turns != 1 {
		t.Fatal("retry duplicated input or started a Turn before preparation", reservations, turns, err)
	}
}

func TestRuntimeWakeHintRejectedSubmitDoesNotAccelerateScan(t *testing.T) {
	for _, name := range []string{"invalid", "idempotency conflict", "competing batch"} {
		t.Run(name, func(t *testing.T) {
			f := newWakeHintIntegration(t)
			key, inputs, want := "wake", wakeHintInput("next turn"), sessions.ErrInvalidInput
			if name == "invalid" {
				inputs = []sessions.Input{{Kind: "unsupported", Payload: json.RawMessage("{}")}}
			} else {
				// Persist directly while the sentinel is blocked. Only the failing
				// Worker submission could emit a hint; Store persistence cannot.
				if _, err := f.fixture.store.ReserveEnvironmentInput(t.Context(), f.target.tenant, f.target.session.ID, key, inputs); err != nil {
					t.Fatal(err)
				}
				if name == "idempotency conflict" {
					inputs, want = wakeHintInput("different input"), sessions.ErrIdempotencyConflict
				} else {
					key, want = "different-key", sessions.ErrTurnConflict
				}
			}
			if _, err := f.worker.SubmitInputs(t.Context(), f.target.tenant, f.target.session.ID, key, inputs); !errors.Is(err, want) {
				t.Fatal("unexpected rejected submission result", err, want)
			}
			f.release()
			// The provider barrier fixes ordering; this short negative window
			// verifies that rejected admission did not queue an immediate scan.
			select {
			case call := <-f.provider.scans:
				t.Fatal("rejected submission accelerated lifecycle scan", call)
			case <-time.After(250 * time.Millisecond):
			}
			f.provider.mu.Lock()
			restores := f.provider.restores
			f.provider.mu.Unlock()
			if restores != 0 {
				t.Fatal("rejected submission triggered restoration", restores)
			}
		})
	}
}

func TestRuntimeWakeHintRunningSubmitDoesNotAccelerateScan(t *testing.T) {
	f := newWakeHintIntegration(t)
	// A completed Turn makes this a subsequent input, so the running compute
	// filter must suppress the hint independently of the initial-input filter.
	f.fixture.complete(f.sentinel.owner)
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	_, err := f.worker.SubmitInputs(ctx, f.sentinel.tenant, f.sentinel.session.ID, "running", wakeHintInput("next turn"))
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("running input did not remain pending", err)
	}
	pending := f.pending(t, f.sentinel, "running")
	if pending.IsInitial {
		t.Fatal("running input unexpectedly exercised initial admission")
	}
	f.release()
	select {
	case call := <-f.provider.scans:
		t.Fatal("running input accelerated lifecycle scan", call)
	case <-time.After(250 * time.Millisecond):
	}
}
