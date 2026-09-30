//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestCapturedSnapshotObservationDoesNotRequireExecutionQualification(t *testing.T) {
	for _, drift := range []string{"cpu", "image", "missing snapshot proof", "foreign snapshot proof"} {
		t.Run(drift, func(t *testing.T) {
			config, actual := deploymentFixture()
			ref := sandbox.Reference{TenantID: "tenant", EnvironmentID: "environment", AllocationID: "allocation"}
			source := wire.Compute{Name: "original", ID: "local:original"}
			labels := wire.Labels(config, ref)
			labels[bootstrapLabel] = "complete"
			actual["labels"] = labels
			snapshotLabels := map[string]string{resourceProofLabel: resourceProof(config)}
			switch drift {
			case "cpu":
				actual["resources"].(map[string]any)["cpus"] = 1
			case "image":
				actual["image"] = "runtime:drifted"
			case "missing snapshot proof":
				delete(snapshotLabels, resourceProofLabel)
			case "foreign snapshot proof":
				snapshotLabels[resourceProofLabel] = "foreign"
			}
			raw, _ := json.Marshal(actual)
			if qualifyConfiguration(config, source, string(raw), false) == nil && qualifySnapshotResources(config, snapshotLabels) == nil {
				t.Fatal("fixture did not disqualify execution")
			}
			// inspectSnapshot has already verified the exact artifact and closure.
			snapshot := wire.SnapshotIdentity{ID: "verified", SourceID: source.ID, SourceName: source.Name}
			reads := 0
			state, err := observeCapturedSnapshot(source, snapshot, func(want wire.Compute) (wire.State, error) {
				reads++
				return qualifyCompute(config, ref, want, source.ID, "paused", string(raw))
			})
			if err != nil || state.Snapshot == nil || *state.Snapshot != snapshot || state.Compute != source || state.SourceStopped || reads != 1 {
				t.Fatal("owned receipt blocked by execution drift", state, err, reads)
			}
		})
	}
}

func TestCapturedSnapshotObservationRetainsSourceOwnershipAndState(t *testing.T) {
	source := wire.Compute{Name: "original", ID: "local:original"}
	snapshot := wire.SnapshotIdentity{ID: "verified", SourceID: source.ID}
	for _, test := range []struct {
		name, status string
		err          error
		stopped      bool
	}{
		{name: "running", status: "running"},
		{name: "unknown", status: "unknown"},
		{name: "stopped", status: "stopped", stopped: true},
		{name: "crashed", status: "crashed", stopped: true},
		{name: "absent", err: &sdk.Error{Kind: sdk.ErrSandboxNotFound}, stopped: true},
		{name: "foreign", err: sandbox.ErrOwnership},
		{name: "unavailable", err: wire.ErrUnconfirmed},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := observeCapturedSnapshot(source, snapshot, func(wire.Compute) (wire.State, error) {
				return wire.State{Compute: source, Status: test.status}, test.err
			})
			if test.err != nil && !test.stopped {
				if !errors.Is(err, test.err) || state.Snapshot != nil {
					t.Fatal("unverified source returned a successful observation", state, err)
				}
				return
			}
			if err != nil || state.Snapshot == nil || state.SourceStopped != test.stopped {
				t.Fatal("observation changed source termination evidence", state, err)
			}
		})
	}
}
