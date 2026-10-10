package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

type observationCaptureProvider struct {
	retentionProvider
	captureError error
}

func (p *observationCaptureProvider) Suspend(_ context.Context, q sandbox.SuspendRequest) (sandbox.ComputeState, error) {
	if p.captureError != nil {
		return sandbox.ComputeState{}, p.captureError
	}
	return sandbox.ComputeState{Compute: q.Source, Snapshot: &sandbox.SnapshotIdentity{ID: "captured"}, Status: "suspended", SourceStopped: true}, nil
}

func TestComputeFailureObservationUsesCommittedReceipt(t *testing.T) {
	for _, stage := range []string{"capture", "source_cleanup"} {
		t.Run(stage, func(t *testing.T) {
			provider := &observationCaptureProvider{}
			if stage == "capture" {
				provider.captureError = sandbox.ErrComputeUnconfirmed
			} else {
				provider.killError = sandbox.ErrComputeUnconfirmed
			}
			fixture := workspaceSettlementFixtureWithSetup(t, provider, workspaceNodeSetup(t, true))
			r, session := fixture.lifecycle, fixture.session
			initial, err := r.provision(t.Context(), session.TenantID, session.Environment.ID, r.config.InstallationID)
			if err != nil {
				t.Fatal(err)
			}
			// Seed the acknowledged idle barrier; subsequent phase commits use the real
			// Session-locked lifecycle operations, with real observation CAS writes.
			if _, err = fixture.pool.Exec(t.Context(), `UPDATE environments SET initialization='complete',status='disconnected' WHERE id=$1`, initial.EnvironmentID); err != nil {
				t.Fatal(err)
			}
			if _, err = fixture.pool.Exec(t.Context(), `UPDATE runtime_allocations SET compute_phase='quiescing',compute_revision=2,compute_retained_until=clock_timestamp()+interval '1 hour' WHERE id=$1`, initial.ID); err != nil {
				t.Fatal(err)
			}
			original, err := r.reader.EnvironmentAllocation(t.Context(), initial.Key())
			if err != nil {
				t.Fatal(err)
			}
			state := runtimeCompute{Current: sandbox.Compute{ID: "source", Name: "source"}, SuspendID: "attempt"}
			until := time.Now().Add(time.Hour)
			suspending, err := r.saveCompute(t.Context(), original, "suspending", state, &until)
			if err != nil {
				t.Fatal(err)
			}
			failure := r.observeCompute(t.Context(), suspending)
			if !errors.Is(failure, sandbox.ErrComputeUnconfirmed) {
				t.Fatalf("capture failure = %v", failure)
			}
			// Model the outer reconcile entry retaining its earlier scan receipt.
			r.recordObservation(t.Context(), original, failure)
			current, err := r.reader.EnvironmentAllocation(t.Context(), initial.Key())
			if err != nil || current.ObservationError != "compute_unconfirmed" {
				t.Fatalf("observation after transition = %q, %v", current.ObservationError, err)
			}
			wantRevision := int64(3)
			if stage == "source_cleanup" {
				wantRevision++
			}
			if current.ComputeRevision != wantRevision {
				t.Fatalf("revision = %d, want %d", current.ComputeRevision, wantRevision)
			}
			// A late result must not borrow a later operation's receipt or erase its
			// diagnostic, even when both observations belong to the same allocation.
			later, err := r.saveCompute(t.Context(), current, "waking", state, &until)
			if err != nil {
				t.Fatal(err)
			}
			r.recordObservation(t.Context(), later, sandbox.ErrOwnership)
			r.recordObservation(t.Context(), original, failure)
			current, err = r.reader.EnvironmentAllocation(t.Context(), initial.Key())
			if err != nil || current.ObservationError != "ownership_mismatch" {
				t.Fatalf("late failure overwrote successor: %q, %v", current.ObservationError, err)
			}
			r.recordObservation(t.Context(), current, nil)
			current, err = r.reader.EnvironmentAllocation(t.Context(), initial.Key())
			if err != nil || current.ObservationError != "" {
				t.Fatalf("current successful observation did not clear diagnostic: %q, %v", current.ObservationError, err)
			}
		})
	}
}
