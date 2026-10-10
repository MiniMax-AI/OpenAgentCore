//go:build linux

package main

import (
	"context"
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

// resume has verified the original full snapshot and its resource proof before
// this step. ObserveOnly may finish this derived receipt, never repeat Restore.
func (b backend) finishRestore(ctx context.Context, target wire.Compute) (wire.State, error) {
	owned, partial, err := b.inspectOwned(ctx, target)
	if err != nil {
		return partial, err
	}
	live, err := owned.Connect(ctx)
	if err != nil {
		return partial, err
	}
	defer live.Detach(context.Background())
	warnings, err := live.RestoreWarnings(ctx)
	if err != nil || len(warnings) != 0 {
		return partial, wire.ErrUnconfirmed
	}
	return finishRestoredTarget(b.q.Config, target, func(exact wire.Compute) (wire.State, string, error) {
		h, state, err := b.inspectOwned(ctx, exact)
		if err != nil {
			return state, "", err
		}
		raw := h.ConfigJSON()
		var actual map[string]any
		if json.Unmarshal([]byte(raw), &actual) != nil {
			return state, "", sandbox.ErrInvalid
		}
		labels, _ := actual["labels"].(map[string]any)
		if labels == nil {
			labels = map[string]any{}
			actual["labels"] = labels
		}
		for key, value := range workspaceLabels(b.q.Workspace) {
			if labels[resourceProofLabel] != nil && labels[resourceProofLabel] != "" && labels[key] != value {
				return state, "", sandbox.ErrOwnership
			}
			if current, exists := labels[key]; exists && current != value {
				return state, "", sandbox.ErrOwnership
			}
			labels[key] = value
		}
		projected, _ := json.Marshal(actual)
		return state, string(projected), nil
	}, func(exact wire.Compute) error {
		// Modify is name-based in the pinned SDK. The allocation flock and
		// ownership checks before and after it retain the exact target fence.
		h, _, err := b.inspectOwned(ctx, exact)
		if err != nil {
			return err
		}
		labels := workspaceLabels(b.q.Workspace)
		labels[resourceProofLabel] = resourceProof(b.q.Config)
		_, err = h.Modify(ctx, sdk.ModifyOptions{Labels: labels, Policy: sdk.ModificationPolicyNextStart})
		return err
	})
}

// The callbacks are only the owned target read and derived label write used by
// restore completion. Neither can create, restore, start or resize compute.
func finishRestoredTarget(config wire.Config, target wire.Compute, readOwned func(wire.Compute) (wire.State, string, error), writeProof func(wire.Compute) error) (wire.State, error) {
	state, raw, err := readOwned(target)
	if err != nil {
		return state, err
	}
	if state.Compute.ID == "" || state.Compute.RestoredFrom == nil || state.Status != "running" || !state.BootstrapComplete {
		return state, wire.ErrUnconfirmed
	}
	// Pin the native ID discovered for the precommitted target name. Every
	// subsequent read and write must still refer to this incarnation.
	target = state.Compute
	if err := qualifyConfiguration(config, target, raw, true); err != nil {
		return state, err
	}
	var actual struct {
		Labels map[string]string `json:"labels"`
	}
	if json.Unmarshal([]byte(raw), &actual) != nil {
		return state, sandbox.ErrInvalid
	}
	proof := actual.Labels[resourceProofLabel]
	if proof != "" && proof != resourceProof(config) {
		return state, sandbox.ErrInvalid
	}
	if proof == "" {
		if err := writeProof(target); err != nil {
			return state, err
		}
	}
	state, raw, err = readOwned(target)
	if err != nil {
		return state, err
	}
	if err := qualifyConfiguration(config, target, raw, false); err != nil {
		return state, err
	}
	if state.Status != "running" || !state.BootstrapComplete {
		return state, wire.ErrUnconfirmed
	}
	return state, nil
}
