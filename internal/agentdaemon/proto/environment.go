package proto

import "github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"

// LocalEnvironment names the bound Environment and its preparation inputs.
// Its workspace is frozen by assignment_bind.
type LocalEnvironment struct {
	ID                string                   `json:"id"`
	CapabilitySources *agentcapabilities.Input `json:"capability_sources"`
	// ToolEnvironment consumes Core-completed confidential initialization.
	ToolEnvironment bool `json:"tool_environment,omitempty"`
}

func (r PromptRequestPayload) EnvironmentID() string {
	if r.LocalEnvironment != nil {
		return r.LocalEnvironment.ID
	}
	return ""
}
