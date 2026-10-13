package execution

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

type restoreAttemptProvider struct {
	retentionProvider
	outcome  string
	requests []sandbox.ResumeRequest
	derived  int
	stop     error
}

func (p *restoreAttemptProvider) NewCompute(_ context.Context, _ sandbox.Reference, generation uint64, snapshot *sandbox.SnapshotIdentity) (sandbox.Compute, error) {
	p.derived++
	return sandbox.Compute{Generation: generation, Name: "target", RestoredFrom: snapshot}, nil
}
func (p *restoreAttemptProvider) Resume(_ context.Context, q sandbox.ResumeRequest) (sandbox.ComputeState, error) {
	p.requests = append(p.requests, q)
	if !q.ObserveOnly {
		return sandbox.ComputeState{}, p.stop
	}
	if p.outcome == "unknown" {
		return sandbox.ComputeState{}, sandbox.ErrComputeUnconfirmed
	}
	closed := q.OperationID
	if p.outcome == "wrong_operation" {
		closed = uuid.NewString()
	}
	result := sandbox.ComputeState{Compute: q.Target, Status: "absent", RestoreAttemptClosed: closed}
	if p.outcome == "wrong_target" {
		result.Compute.Name = "another-target"
	}
	return result, nil
}

func TestRestoreAttemptRecoveryUsesExactClosedEvidence(t *testing.T) {
	for _, outcome := range []string{"unsent_identity", "closed", "unknown", "wrong_operation", "wrong_target"} {
		t.Run(outcome, func(t *testing.T) {
			stop := errors.New("native request captured")
			provider := &restoreAttemptProvider{outcome: outcome, stop: stop}
			f := workspaceSettlementFixtureWithSetup(t, provider, workspaceNodeSetup(t, true))
			r, session := f.lifecycle, f.session
			owner, err := r.provision(t.Context(), session.TenantID, session.Environment.ID, r.config.InstallationID)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := &sandbox.SnapshotIdentity{ID: "snapshot", Compatibility: sandbox.CheckpointCompatibility{ArtifactDomain: "fixture-store", ExecutionClass: "fixture-runtime"}}
			state := runtimeCompute{Current: sandbox.Compute{ID: "source", Name: "source", Generation: 1}, Snapshot: snapshot, RestoreID: uuid.NewString()}
			if outcome != "unsent_identity" {
				state.Target = &sandbox.Compute{Name: "target", Generation: 2, RestoredFrom: snapshot}
			}
			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_phase='restoring',compute_revision=4,compute_retained_until=clock_timestamp()+interval '1 hour',compute_state=$2 WHERE id=$1`, owner.ID, raw); err != nil {
				t.Fatal(err)
			}
			owner, err = r.reader.EnvironmentAllocation(t.Context(), owner.Key())
			if err != nil {
				t.Fatal(err)
			}
			err = r.restoreCompute(t.Context(), provider, owner, state, true)
			current, readErr := r.reader.EnvironmentAllocation(t.Context(), owner.Key())
			if readErr != nil {
				t.Fatal(readErr)
			}
			var persisted runtimeCompute
			if json.Unmarshal(current.ComputeState, &persisted) != nil {
				t.Fatal("invalid persisted receipt")
			}
			switch outcome {
			case "unsent_identity":
				if !errors.Is(err, stop) || provider.derived != 1 || len(provider.requests) != 1 || provider.requests[0].ObserveOnly || persisted.Target == nil || persisted.RestoreID != state.RestoreID {
					t.Fatalf("unsent intent not safely derived: %v %#v", err, provider.requests)
				}
			case "closed":
				if !errors.Is(err, stop) || len(provider.requests) != 2 || !provider.requests[0].ObserveOnly || provider.requests[1].ObserveOnly || persisted.RestoreID == state.RestoreID || provider.requests[1].OperationID != persisted.RestoreID {
					t.Fatalf("closed attempt not replaced durably: %v %#v", err, provider.requests)
				}
			default:
				want := sandbox.ErrOwnership
				if outcome == "unknown" {
					want = sandbox.ErrComputeUnconfirmed
				}
				if !errors.Is(err, want) || len(provider.requests) != 1 || persisted.RestoreID != state.RestoreID || current.ComputeRevision != owner.ComputeRevision {
					t.Fatalf("unproven attempt was replayed: %v %#v", err, provider.requests)
				}
			}
		})
	}
}

func TestCheckpointTransferRoutesBeforeTargetIO(t *testing.T) {
	for _, operation := range []string{"wake", "expiry"} {
		t.Run(operation, func(t *testing.T) {
			stop := errors.New("target Resume captured")
			provider := &restoreAttemptProvider{stop: stop}
			f := workspaceSettlementFixtureWithSetup(t, provider, workspaceNodeSetup(t, true))
			r, session := f.lifecycle, f.session
			owner, err := r.provision(t.Context(), session.TenantID, session.Environment.ID, r.config.InstallationID)
			if err != nil {
				t.Fatal(err)
			}
			view, err := r.deployments.View(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			token, err := r.deployments.CreateEnrollment(t.Context(), deployment.Capacity{MaxActive: 1, MaxRetained: 2})
			if err != nil {
				t.Fatal(err)
			}
			targetNode := uuid.NewString()
			node := deployment.Enrollment{NodeID: targetNode, Name: "checkpoint target", Credential: strings.Repeat("t", 64), Provider: view.Provider, BackendFingerprint: strings.Repeat("c", 64), DeploymentGeneration: view.Generation, SpecificationDigest: view.SpecificationDigest, CoreURL: fixturePublicURL}
			if _, err = r.deployments.Enroll(t.Context(), token.Token, node); err != nil {
				t.Fatal(err)
			}
			connection := uuid.NewString()
			if err = r.deployments.ConnectNode(t.Context(), targetNode, connection, view.OwnerEpoch); err != nil {
				t.Fatal(err)
			}
			compat := sandbox.CheckpointCompatibility{ArtifactDomain: "fixture-store", ExecutionClass: "fixture-runtime"}
			if err = r.deployments.Heartbeat(t.Context(), targetNode, connection, view.OwnerEpoch, deployment.NodeHealth{ProviderReady: true}, []sandbox.GenerationStatus{{Generation: view.Generation, SpecificationDigest: view.SpecificationDigest, State: "ready", Checkpoint: &compat}}); err != nil {
				t.Fatal(err)
			}
			state := runtimeCompute{Current: sandbox.Compute{ID: "source", Name: "source", Generation: 1}, Snapshot: &sandbox.SnapshotIdentity{ID: "snapshot", Compatibility: compat}}
			raw, _ := json.Marshal(state)
			if _, err = f.pool.Exec(t.Context(), `UPDATE environments SET initialization='complete',status='disconnected' WHERE id=$1`, owner.EnvironmentID); err != nil {
				t.Fatal(err)
			}
			if _, err = f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_phase='suspended',compute_state=$2,compute_revision=4,compute_retained_until=clock_timestamp()+interval '1 hour' WHERE id=$1`, owner.ID, raw); err != nil {
				t.Fatal(err)
			}
			if _, err = f.pool.Exec(t.Context(), `UPDATE runtime_nodes SET last_seen_at=clock_timestamp()-interval '1 minute' WHERE id=$1`, owner.NodeID); err != nil {
				t.Fatal(err)
			}
			if operation == "wake" {
				err = r.deployments.TouchActivity(t.Context(), owner.TenantID, owner.EnvironmentID)
			} else {
				_, err = f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_retained_until=clock_timestamp()-interval '1 second' WHERE id=$1`, owner.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			source, err := r.reader.EnvironmentAllocation(t.Context(), owner.Key())
			if err != nil {
				t.Fatal(err)
			}
			hints := 0
			r.hintDestination = func(id string) {
				hints++
				committed, err := r.reader.EnvironmentAllocation(t.Context(), owner.Key())
				if err != nil || id != targetNode || committed.NodeID != id || committed.ID != owner.ID {
					t.Fatal("hint preceded durable route or targeted wrong node", committed, err)
				}
			}
			if err = r.observe(t.Context(), source); err != nil {
				t.Fatal(err)
			}
			moved, err := r.reader.EnvironmentAllocation(t.Context(), owner.Key())
			if err != nil {
				t.Fatal(err)
			}
			if moved.NodeID != targetNode || moved.ID != owner.ID || moved.DeviceID != owner.DeviceID || provider.derived != 0 || len(provider.requests) != 0 || provider.kills != 0 || provider.snapshots != 0 {
				t.Fatal("source lane executed target effects or lost identity", moved)
			}
			if hints != 1 {
				t.Fatalf("expected one committed handoff hint, got %d", hints)
			}
			// Simulate the independently scheduled target lane after the durable handoff.
			r.nodeID = targetNode
			err = r.observe(t.Context(), moved)
			if operation == "wake" {
				if !errors.Is(err, stop) || provider.derived != 1 || len(provider.requests) != 1 || provider.requests[0].ObserveOnly {
					t.Fatal("target failed to use committed unsent restore", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				released, err := r.reader.EnvironmentAllocation(t.Context(), owner.Key())
				if err != nil || released.State != "released" || provider.kills != 0 || provider.snapshots != 1 {
					t.Fatal("offline source cleanup did not settle at archive domain", released, err)
				}
			}
		})
	}
}

// Archive bytes may be unusable while the exact durable ownership receipt is
// still valid for deletion. Observing it must not authorize a new capture.
type cleanupReceiptProvider struct {
	retentionProvider
	observed bool
}

func (p *cleanupReceiptProvider) Suspend(_ context.Context, q sandbox.SuspendRequest) (sandbox.ComputeState, error) {
	p.observed = q.ObserveOnly
	if !q.ObserveOnly {
		return sandbox.ComputeState{}, sandbox.ErrComputeUnconfirmed
	}
	return sandbox.ComputeState{Compute: q.Source, Status: "unknown", Snapshot: &sandbox.SnapshotIdentity{ID: "owned-corrupt-archive", OperationID: q.OperationID, Compatibility: sandbox.CheckpointCompatibility{ArtifactDomain: "fixture-store", ExecutionClass: "fixture-runtime"}}}, nil
}
func TestLostCaptureReceiptCanStillBeDeleted(t *testing.T) {
	p := &cleanupReceiptProvider{}
	f := workspaceSettlementFixtureWithSetup(t, p, workspaceNodeSetup(t, true))
	r, s := f.lifecycle, f.session
	owner, err := r.provision(t.Context(), s.TenantID, s.Environment.ID, r.config.InstallationID)
	if err != nil {
		t.Fatal(err)
	}
	state := runtimeCompute{Current: sandbox.Compute{ID: "source", Name: "source"}, SuspendID: uuid.NewString()}
	raw, _ := json.Marshal(state)
	if _, err = f.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_phase='suspending',compute_state=$2,compute_revision=4,compute_retained_until=clock_timestamp()-interval '1 second' WHERE id=$1`, owner.ID, raw); err != nil {
		t.Fatal(err)
	}
	owner, err = r.reader.EnvironmentAllocation(t.Context(), owner.Key())
	if err != nil {
		t.Fatal(err)
	}
	if err = r.observe(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	current, err := r.reader.EnvironmentAllocation(t.Context(), owner.Key())
	if err != nil || current.State != "released" || !p.observed || p.kills != 1 || p.snapshots != 1 {
		t.Fatal("exact cleanup receipt was discarded", current, err, p.observed, p.kills, p.snapshots)
	}
}
