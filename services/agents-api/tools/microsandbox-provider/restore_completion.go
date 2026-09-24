//go:build linux

package main

import (
	"context"
	"encoding/json"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	wire "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

// resume has verified the original full snapshot and its resource proof before
// this step. ObserveOnly may finish this derived receipt, never repeat Restore.
func (b backend) finishRestore(ctx context.Context, target wire.Compute) (wire.State, error) {
	return finishRestoredTarget(b.q.Config, target, func(exact wire.Compute) (wire.State, string, error) {
		h, state, err := b.inspectOwned(ctx, exact)
		if err != nil {
			return state, "", err
		}
		return state, h.ConfigJSON(), nil
	}, func(exact wire.Compute) error {
		// Modify is name-based in the pinned SDK. The allocation flock and
		// ownership checks before and after it retain the exact target fence.
		h, _, err := b.inspectOwned(ctx, exact)
		if err != nil {
			return err
		}
		_, err = h.Modify(ctx, sdk.ModifyOptions{Labels: map[string]string{resourceProofLabel: resourceProof(b.q.Config)}, Policy: sdk.ModificationPolicyNextStart})
		return err
	})
}

// The callbacks are only the owned target read and derived label write used by
// restore completion. Neither can create, restore, start or resize compute.
func finishRestoredTarget(config wire.Config, target wire.Compute, readOwned func(wire.Compute) (wire.State, string, error), writeProof func(wire.Compute) error) (wire.State, error) {
	state, raw, err := readOwned(target)
	if err != nil {
		return wire.State{}, err
	}
	if state.Compute.ID == "" || state.Compute.RestoredFrom == nil || state.Status != "running" || !state.BootstrapComplete {
		return wire.State{}, wire.ErrUnconfirmed
	}
	// Pin the native ID discovered for the precommitted target name. Every
	// subsequent read and write must still refer to this incarnation.
	target = state.Compute
	if err := qualifyConfiguration(config, target, raw, true); err != nil {
		return wire.State{}, err
	}
	var actual struct {
		Labels map[string]string `json:"labels"`
	}
	if json.Unmarshal([]byte(raw), &actual) != nil {
		return wire.State{}, sandbox.ErrInvalid
	}
	proof := actual.Labels[resourceProofLabel]
	if proof != "" && proof != resourceProof(config) {
		return wire.State{}, sandbox.ErrInvalid
	}
	if proof == "" {
		if err := writeProof(target); err != nil {
			return wire.State{}, err
		}
	}
	state, raw, err = readOwned(target)
	if err != nil {
		return wire.State{}, err
	}
	if err := qualifyConfiguration(config, target, raw, false); err != nil {
		return wire.State{}, err
	}
	if state.Status != "running" || !state.BootstrapComplete {
		return wire.State{}, wire.ErrUnconfirmed
	}
	return state, nil
}
