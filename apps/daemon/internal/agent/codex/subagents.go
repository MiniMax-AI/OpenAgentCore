package codex

import "slices"

func disableSubagents(plan *SessionPlan) {
	// Disable both native Subagent feature versions.
	for _, feature := range []string{"multi_agent", "multi_agent_v2"} {
		if !slices.Contains(plan.DisableFeatures, feature) {
			plan.DisableFeatures = append(plan.DisableFeatures, feature)
		}
	}
}
