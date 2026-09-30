package api

import (
	"encoding/json"
	"fmt"
)

func (h *Handler) sessionHarness(raw json.RawMessage) (string, error) {
	var cfg configuration
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", err
	}
	if cfg.Agent.XAgentsCore == nil || cfg.Agent.XAgentsCore.Harness == "" {
		return h.Engine, nil
	}
	kind := cfg.Agent.XAgentsCore.Harness
	if kind != h.Engine && !h.harnesses[kind] {
		return "", fmt.Errorf("Harness %s is not enabled on this Core deployment.", kind)
	}
	return kind, nil
}
