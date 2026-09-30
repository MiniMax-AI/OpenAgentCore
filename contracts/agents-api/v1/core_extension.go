package v1

import (
	"encoding/json"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
)

// AgentsCore selects an existing Core harness independently of model identity.
type AgentsCore struct {
	Harness       string          `json:"harness,omitempty"`
	HarnessConfig json.RawMessage `json:"harness_config,omitempty" swaggertype:"object"`
}

func (x *AgentsCore) Validate() error {
	if x == nil {
		return nil
	}
	if x.Harness == "" && len(x.HarnessConfig) == 0 {
		return fmt.Errorf("x_agents_core requires harness or harness_config")
	}
	if x.Harness != "" && !builtin.Contains(x.Harness) {
		return fmt.Errorf("x_agents_core.harness must select a registered harness")
	}
	return ValidateHarnessConfig(x.Harness, x.HarnessConfig)
}
