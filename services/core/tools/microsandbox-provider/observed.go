//go:build linux

package main

import (
	"encoding/json"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
)

func qualifyCompute(deployment wire.Config, reference sandbox.Reference, c wire.Compute, actualID, status, raw string) (wire.State, error) {
	state := wire.State{Compute: c}
	if c.ID != "" && actualID != c.ID {
		return state, sandbox.ErrOwnership
	}
	var config struct {
		Labels            map[string]string `json:"labels"`
		SnapshotParent    string            `json:"snapshot_parent"`
		CheckpointRestore json.RawMessage   `json:"checkpoint_restore"`
	}
	if json.Unmarshal([]byte(raw), &config) != nil {
		return state, sandbox.ErrOwnership
	}
	if c.RestoredFrom == nil {
		for key, value := range wire.Labels(deployment, reference) {
			if config.Labels[key] != value {
				return state, sandbox.ErrOwnership
			}
		}
		if config.SnapshotParent != "" {
			return state, sandbox.ErrOwnership
		}
		state.BootstrapComplete = config.Labels[bootstrapLabel] == "complete"
	} else {
		if config.SnapshotParent != c.RestoredFrom.ID {
			return state, sandbox.ErrOwnership
		}
		state.BootstrapComplete = len(config.CheckpointRestore) == 0 || string(config.CheckpointRestore) == "null"
	}
	state.Compute.ID = actualID
	state.Status = status
	return state, nil
}
