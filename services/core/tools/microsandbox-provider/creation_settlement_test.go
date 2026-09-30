//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
)

func TestCreatedConfigurationRejectionRequiresExactOwnedCompute(t *testing.T) {
	for _, name := range []string{"matching", "resource drift", "image drift", "foreign ID", "foreign labels", "missing created ID", "bootstrap complete", "absent", "malformed"} {
		t.Run(name, func(t *testing.T) {
			config, actual := deploymentFixture()
			ref := sandbox.Reference{TenantID: "tenant", EnvironmentID: "environment", AllocationID: "allocation"}
			created := wire.Compute{Name: wire.Name(config, ref, 0), ID: "local:created"}
			labels := wire.Labels(config, ref)
			labels[bootstrapLabel] = "pending"
			actual["labels"] = labels
			actualID, status := created.ID, "running"
			switch name {
			case "resource drift":
				actual["resources"].(map[string]any)["cpus"] = 1
			case "image drift":
				actual["image"] = "runtime:latest"
			case "foreign ID":
				actualID = "local:foreign"
			case "foreign labels":
				actual["labels"] = map[string]string{}
			case "missing created ID":
				created.ID = ""
			case "bootstrap complete":
				labels[bootstrapLabel] = "complete"
			case "absent":
				status = "absent"
			}
			raw, _ := json.Marshal(actual)
			if name == "malformed" {
				raw = []byte(`{"labels":`)
			}
			result, err := qualifyCreatedConfiguration(config, ref, created, actualID, status, string(raw))
			settled := name == "resource drift" || name == "image drift"
			if result.CreateSettled != settled || (err == nil) != (name == "matching") {
				t.Fatal("configuration result changed creation proof", result, err)
			}
			if settled {
				if !errors.Is(err, sandbox.ErrInvalid) || result.State == nil || result.State.Compute != created || result.State.BootstrapComplete {
					t.Fatal("rejection did not preserve exact unbootstrapped compute", result, err)
				}
			} else if name != "matching" && result.State != nil {
				t.Fatal("unverified compute returned a receipt", result)
			}
		})
	}
}
