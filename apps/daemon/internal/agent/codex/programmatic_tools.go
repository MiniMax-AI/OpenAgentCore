package codex

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

var programmaticFeatures = []string{"code_mode", "code_mode_only", "code_mode_prewarm"}

// applyProgrammaticTools maps the official programmatic_tool_calling.enabled
// value to Codex code mode, whose model-written JavaScript calls the other
// tools. Disabling also turns off its dependent features. Omission keeps the
// native default.
func applyProgrammaticTools(plan *SessionPlan, controls *proto.ExecutionControls) {
	if controls == nil || controls.ProgrammaticToolCalling == nil {
		return
	}
	if *controls.ProgrammaticToolCalling {
		plan.EnableFeatures = append(plan.EnableFeatures, "code_mode")
		plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"features.code_mode", "true"})
		return
	}
	for _, feature := range programmaticFeatures {
		if !slices.Contains(plan.DisableFeatures, feature) {
			plan.DisableFeatures = append(plan.DisableFeatures, feature)
		}
		plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"features." + feature, "false"})
	}
}

// Managed native requirements can override command-line feature settings.
// Check before starting or resuming a native Session, not after model execution.
func verifyProgrammaticTools(ctx context.Context, rpc *JSONRPCClient, enabled bool) error {
	raw, err := rpc.Request(ctx, "configRequirements/read", nil)
	var response struct {
		Requirements json.RawMessage `json:"requirements"`
	}
	if err != nil || json.Unmarshal(raw, &response) != nil || len(response.Requirements) == 0 {
		return errors.New("codex: programmatic tool requirements unavailable")
	}
	if string(response.Requirements) != "null" {
		var requirements struct {
			Features map[string]bool `json:"featureRequirements"`
		}
		if json.Unmarshal(response.Requirements, &requirements) != nil {
			return errors.New("codex: invalid programmatic tool requirements")
		}
		if enabled {
			if required, pinned := requirements.Features["code_mode"]; pinned && !required {
				return errors.New("codex: required programmatic tools conflict with explicit enabling")
			}
			return nil
		}
		for _, feature := range programmaticFeatures {
			if requirements.Features[feature] {
				return errors.New("codex: required programmatic tools conflict with explicit disabling")
			}
		}
	}
	return nil
}
