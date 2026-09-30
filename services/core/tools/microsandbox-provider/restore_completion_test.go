//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
)

func TestRestoreCompletionRecoversOnlyDerivedProof(t *testing.T) {
	for _, failure := range []string{"before proof write", "lost proof acknowledgement"} {
		t.Run(failure, func(t *testing.T) {
			config, actual := deploymentFixture()
			actual["image"].(map[string]any)["Oci"].(map[string]any)["root_disk"].(map[string]any)["size_mib"] = nil
			actual["snapshot_parent"] = "verified-snapshot"
			labels := map[string]string{}
			actual["labels"] = labels
			target := wire.Compute{Name: "restored-target", Generation: 1, RestoredFrom: &wire.SnapshotIdentity{ID: "verified-snapshot"}}
			reads, writes := 0, 0
			readOwned := func(want wire.Compute) (wire.State, string, error) {
				reads++
				raw, _ := json.Marshal(actual)
				state, err := qualifyCompute(config, sandbox.Reference{}, want, "local:restored", "running", string(raw))
				return state, string(raw), err
			}
			interrupted := errors.New("interrupted completion")
			writeProof := func(exact wire.Compute) error {
				writes++
				if exact.ID != "local:restored" || exact.Name != target.Name || exact.RestoredFrom.ID != target.RestoredFrom.ID {
					t.Fatal("proof write lost exact restored identity", exact)
				}
				if writes == 1 {
					if failure == "lost proof acknowledgement" {
						labels[resourceProofLabel] = resourceProof(config)
					}
					return interrupted
				}
				labels[resourceProofLabel] = resourceProof(config)
				return nil
			}
			if _, err := finishRestoredTarget(config, target, readOwned, writeProof); !errors.Is(err, interrupted) {
				t.Fatal("interrupted write became successful restore", err)
			}
			state, err := finishRestoredTarget(config, target, readOwned, writeProof)
			if err != nil || state.Compute.ID != "local:restored" || !state.BootstrapComplete || state.Status != "running" {
				t.Fatal("same target could not finish its receipt", state, err)
			}
			wantWrites := 2
			if failure == "lost proof acknowledgement" {
				wantWrites = 1
			}
			if writes != wantWrites || reads != 3 {
				t.Fatal("completion repeated proof or skipped strict reread", writes, reads)
			}
		})
	}
}

func TestRestoreCompletionRejectsUnverifiedTargets(t *testing.T) {
	for _, fault := range []string{"cpu", "memory", "environment", "image", "root", "foreign parent", "foreign ID", "foreign proof", "stopped", "unfinished", "replacement after write", "proof not persisted"} {
		t.Run(fault, func(t *testing.T) {
			config, actual := deploymentFixture()
			actual["image"].(map[string]any)["Oci"].(map[string]any)["root_disk"].(map[string]any)["size_mib"] = nil
			actual["snapshot_parent"] = "verified-snapshot"
			labels := map[string]string{}
			actual["labels"] = labels
			target := wire.Compute{Name: "restored-target", ID: "local:restored", Generation: 1, RestoredFrom: &wire.SnapshotIdentity{ID: "verified-snapshot"}}
			actualID, status := target.ID, "running"
			switch fault {
			case "cpu":
				actual["resources"].(map[string]any)["cpus"] = 1
			case "memory":
				actual["resources"].(map[string]any)["memory_mib"] = 1024
			case "environment":
				actual["mounts"].([]any)[0].(map[string]any)["storage"].(map[string]any)["capacity_mib"] = 1024
			case "image":
				actual["image"] = "runtime:foreign"
			case "root":
				actual["image"].(map[string]any)["Oci"].(map[string]any)["root_disk"].(map[string]any)["size_mib"] = 1024
			case "foreign parent":
				actual["snapshot_parent"] = "foreign-snapshot"
			case "foreign ID":
				actualID = "local:foreign"
			case "foreign proof":
				labels[resourceProofLabel] = "foreign"
			case "stopped":
				status = "stopped"
			case "unfinished":
				actual["checkpoint_restore"] = map[string]string{"checkpoint_id": "pending"}
			}
			writes := 0
			readOwned := func(want wire.Compute) (wire.State, string, error) {
				raw, _ := json.Marshal(actual)
				state, err := qualifyCompute(config, sandbox.Reference{}, want, actualID, status, string(raw))
				return state, string(raw), err
			}
			writeProof := func(wire.Compute) error {
				writes++
				if fault != "proof not persisted" {
					labels[resourceProofLabel] = resourceProof(config)
				}
				if fault == "replacement after write" {
					actualID = "local:foreign"
				}
				return nil
			}
			if _, err := finishRestoredTarget(config, target, readOwned, writeProof); err == nil {
				t.Fatal("unqualified restored target admitted")
			}
			wantWrites := 0
			if fault == "replacement after write" || fault == "proof not persisted" {
				wantWrites = 1
			}
			if writes != wantWrites {
				t.Fatal("unverified target received a derived proof", writes)
			}
		})
	}
}

func TestRestoreCompletionPreservesInspectionErrors(t *testing.T) {
	for _, failedRead := range []int{1, 2} {
		config, actual := deploymentFixture()
		actual["snapshot_parent"] = "verified-snapshot"
		actual["labels"] = map[string]string{resourceProofLabel: resourceProof(config)}
		target := wire.Compute{Name: "restored-target", ID: "local:restored", Generation: 1, RestoredFrom: &wire.SnapshotIdentity{ID: "verified-snapshot"}}
		unavailable := errors.New("native inspection unavailable")
		reads := 0
		_, err := finishRestoredTarget(config, target, func(want wire.Compute) (wire.State, string, error) {
			reads++
			if reads == failedRead {
				return wire.State{}, "", unavailable
			}
			raw, _ := json.Marshal(actual)
			state, err := qualifyCompute(config, sandbox.Reference{}, want, target.ID, "running", string(raw))
			return state, string(raw), err
		}, func(wire.Compute) error {
			t.Fatal("existing proof must not be rewritten")
			return nil
		})
		if !errors.Is(err, unavailable) || reads != failedRead {
			t.Fatal("inspection failure lost or retried", err, reads)
		}
	}
}
