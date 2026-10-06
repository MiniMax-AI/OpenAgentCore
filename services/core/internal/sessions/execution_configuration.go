package sessions

import (
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// NormalizeExecutionProjection completes a Session's frozen execution
// configuration for sessionID as it is stored and read: the object and schema
// version, an unknown harness configuration source, the resolved harness
// configuration, and the provider view only while it is available. A
// deployment provider frozen without its safe view stays redacted.
func NormalizeExecutionProjection(projection *v1.SessionExecutionConfiguration, sessionID string) {
	projection.Object = "agent.session.execution_configuration"
	projection.SchemaVersion = 1
	if projection.HarnessConfig.Source == "" {
		projection.HarnessConfig.Source = "unknown"
	}
	projection.HarnessConfig.Value = v1.ResolvedHarnessConfig(projection.HarnessConfig.Value)
	projection.SessionID = sessionID
	if projection.ModelProvider.Source == "deployment" && (projection.ModelProvider.Status != "available" || projection.ModelProvider.Configuration == nil) {
		// Sessions created before deployment defaults moved into Core stay redacted.
		projection.ModelProvider.Status = "redacted"
		projection.ModelProvider.Configuration = nil
	} else if projection.ModelProvider.Status != "available" {
		projection.ModelProvider.Configuration = nil
	}
}

// ExecutionModel returns the model a stored Session configuration selects,
// nil when it selects none.
func ExecutionModel(configuration []byte) (*string, error) {
	var config struct {
		Agent struct {
			Model *string `json:"model"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(configuration, &config); err != nil {
		return nil, errors.New("invalid stored Session model configuration")
	}
	return config.Agent.Model, nil
}
