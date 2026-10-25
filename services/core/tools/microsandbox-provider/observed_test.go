//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
	"testing"
)

func TestRestoredOwnershipUsesExactParentWithoutLabels(t *testing.T) {
	c := wire.Compute{ID: "local:5", RestoredFrom: &wire.SnapshotIdentity{ID: "snap_exact"}}
	for _, tc := range []struct {
		name, raw string
		complete  bool
		rejected  bool
	}{
		{"exact", `{"labels":{},"snapshot_parent":"snap_exact"}`, true, false},
		{"other", `{"labels":{},"snapshot_parent":"snap_foreign"}`, false, true},
		{"unfinished", `{"snapshot_parent":"snap_exact","checkpoint_restore":{"checkpoint_id":"x"}}`, false, false},
		{"absent", `{"labels":{}}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, e := qualifyCompute(wire.Config{RuntimeHome: t.TempDir()}, sandbox.Reference{}, c, "local:5", "running", tc.raw)
			if tc.rejected {
				if !errors.Is(e, sandbox.ErrOwnership) {
					t.Fatalf("foreign accepted: %v", e)
				}
				return
			}
			if e != nil || state.BootstrapComplete != tc.complete {
				t.Fatalf("state=%+v error=%v", state, e)
			}
		})
	}
}
func TestInitialIdentityNeedsAllLabelsAndBootstrapReceipt(t *testing.T) {
	config := wire.Config{InstallationID: "installation"}
	ref := sandbox.Reference{TenantID: "tenant", EnvironmentID: "environment", AllocationID: "allocation"}
	labels := wire.Labels(config, ref)
	labels[bootstrapLabel] = "pending"
	raw, _ := json.Marshal(map[string]any{"labels": labels})
	state, e := qualifyCompute(config, ref, wire.Compute{ID: "local:4"}, "local:4", "running", string(raw))
	if e != nil || state.BootstrapComplete {
		t.Fatalf("running implied bootstrap completion: %+v %v", state, e)
	}
	labels[bootstrapLabel] = "complete"
	delete(labels, "io.oac.tenant")
	raw, _ = json.Marshal(map[string]any{"labels": labels})
	_, e = qualifyCompute(config, ref, wire.Compute{ID: "local:4"}, "local:4", "running", string(raw))
	if !errors.Is(e, sandbox.ErrOwnership) {
		t.Fatalf("missing owner accepted: %v", e)
	}
}
func TestSameNameReplacementCannotBeAdopted(t *testing.T) {
	_, e := qualifyCompute(wire.Config{}, sandbox.Reference{}, wire.Compute{ID: "local:old"}, "local:new", "running", `{}`)
	if !errors.Is(e, sandbox.ErrOwnership) {
		t.Fatalf("replacement accepted: %v", e)
	}
}
func TestOnlyObservedTerminalStatesProveSourceStopped(t *testing.T) {
	for _, s := range []sdk.SandboxStatus{sdk.SandboxStatusCreated, sdk.SandboxStatusStarting, sdk.SandboxStatusRunning, sdk.SandboxStatusDraining, sdk.SandboxStatusPaused, "unknown"} {
		if terminal(s) {
			t.Fatalf("%s falsely proves memory release", s)
		}
	}
	if !terminal(sdk.SandboxStatusStopped) || !terminal(sdk.SandboxStatusCrashed) {
		t.Fatal("terminal states not recognized")
	}
}
