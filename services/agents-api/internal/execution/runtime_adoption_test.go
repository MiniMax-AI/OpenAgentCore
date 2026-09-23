package execution

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// Unimplemented mutation methods panic: adoption may only make these observations.
type adoptionObserver struct {
	sandbox.CheckpointProvider
	info     func(sandbox.Reference) (sandbox.Info, error)
	compute  func(sandbox.Compute) (sandbox.ComputeState, error)
	snapshot func(sandbox.SuspendRequest) (sandbox.ComputeState, error)
}

func (p adoptionObserver) GetInfo(_ context.Context, r sandbox.Reference) (sandbox.Info, error) {
	return p.info(r)
}
func (p adoptionObserver) GetCompute(_ context.Context, _ sandbox.Reference, c sandbox.Compute) (sandbox.ComputeState, error) {
	return p.compute(c)
}
func (p adoptionObserver) Suspend(_ context.Context, q sandbox.SuspendRequest) (sandbox.ComputeState, error) {
	return p.snapshot(q)
}

func TestLegacyOwnershipBaseRequiresPositiveIdentity(t *testing.T) {
	for _, state := range []string{"running", "exited", "missing", "foreign", "unavailable"} {
		t.Run(state, func(t *testing.T) {
			p := adoptionObserver{info: func(r sandbox.Reference) (sandbox.Info, error) {
				switch state {
				case "missing":
					return sandbox.Info{}, sandbox.ErrNotFound
				case "foreign":
					return sandbox.Info{}, sandbox.ErrOwnership
				case "unavailable":
					return sandbox.Info{}, errors.New("private endpoint and credentials")
				}
				return sandbox.Info{Reference: r, ProviderID: "exact-container", State: state}, nil
			}}
			err := VerifyLegacyRuntimeOwnership(t.Context(), p, store.RuntimeAllocation{ComputePhase: "disabled"})
			expected := map[string]string{"missing": "resource_missing", "foreign": "ownership_mismatch", "unavailable": "provider_unavailable"}[state]
			if expected == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != expected {
				t.Fatal(err)
			}
		})
	}
}
func TestLegacyOwnershipCheckpointEvidenceMatrix(t *testing.T) {
	for _, kind := range []string{"running", "stopped", "absent", "snapshot_only", "target_absent", "current_foreign", "target_foreign", "snapshot_corrupt", "snapshot_mismatch", "snapshot_missing", "consumed", "consumed_source_foreign", "consumed_later_generation"} {
		t.Run(kind, func(t *testing.T) {
			current := sandbox.Compute{Name: "source", ID: "source-id"}
			snapshot := sandbox.SnapshotIdentity{Reference: "artifact", ID: "snapshot-id", Digest: "digest", CheckpointID: "checkpoint", CheckpointRoot: "root", OperationID: "capture", SourceName: current.Name, SourceID: current.ID}
			target := sandbox.Compute{Name: "target", ID: "target-id", Generation: 1, RestoredFrom: &snapshot}
			receipt := runtimeCompute{Current: current}
			if kind != "running" && kind != "stopped" && kind != "absent" {
				receipt.Snapshot = &snapshot
				receipt.SuspendID = "capture"
			}
			if kind == "target_foreign" || kind == "target_absent" {
				receipt.Target = &target
			}
			if kind == "consumed" || kind == "consumed_source_foreign" || kind == "consumed_later_generation" {
				receipt.Current = target
				receipt.RestoreID = "restore"
				if kind == "consumed_later_generation" {
					snapshot.SourceGeneration = 1
				}
			}
			p := adoptionObserver{
				compute: func(c sandbox.Compute) (sandbox.ComputeState, error) {
					if kind == "absent" || kind == "snapshot_only" || (kind == "target_absent" && c.Name == target.Name) || (kind == "consumed" && c.Name == current.Name) {
						return sandbox.ComputeState{}, sandbox.ErrNotFound
					}
					if (kind == "current_foreign" && c.Name == current.Name) || (kind == "target_foreign" && c.Name == target.Name) || (kind == "consumed_source_foreign" && c.Name == current.Name) {
						return sandbox.ComputeState{}, sandbox.ErrOwnership
					}
					return sandbox.ComputeState{Compute: c, Status: kind}, nil
				},
				snapshot: func(q sandbox.SuspendRequest) (sandbox.ComputeState, error) {
					if !q.ObserveOnly || q.OperationID != "capture" || q.Source != current {
						t.Fatal("mutating or wrong capture observation", q)
					}
					if kind == "snapshot_corrupt" || kind == "snapshot_missing" {
						return sandbox.ComputeState{}, sandbox.ErrComputeUnconfirmed
					}
					got := snapshot
					if kind == "snapshot_mismatch" {
						got.Digest = "different"
					}
					return sandbox.ComputeState{Compute: current, Status: "suspended", Snapshot: &got, SourceStopped: true}, nil
				},
			}
			raw, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			err = VerifyLegacyRuntimeOwnership(t.Context(), p, store.RuntimeAllocation{ComputePhase: "suspended", ComputeState: raw})
			expected := map[string]string{"absent": "resource_missing", "current_foreign": "ownership_mismatch", "target_foreign": "ownership_mismatch", "snapshot_corrupt": "snapshot_unconfirmed", "snapshot_missing": "snapshot_unconfirmed", "snapshot_mismatch": "ownership_mismatch", "consumed": "resource_unconfirmed", "consumed_source_foreign": "resource_unconfirmed", "consumed_later_generation": "resource_unconfirmed"}[kind]
			if expected == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != expected {
				t.Fatal(err)
			}
		})
	}
}
