package proto

import "github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"

// LocalEnvironment names a frozen workspace selection. Runtime must verify it
// against the bound local root before resolving capabilities or native execution.
type LocalEnvironment struct {
	ID                 string                   `json:"id"`
	WorkspaceDirectory string                   `json:"workspace_directory"`
	CapabilitySources  *agentcapabilities.Input `json:"capability_sources"`
	// ToolEnvironment consumes Core-completed confidential initialization.
	ToolEnvironment bool `json:"tool_environment,omitempty"`
}

func (r PromptRequestPayload) EnvironmentID() string {
	if r.LocalEnvironment != nil {
		return r.LocalEnvironment.ID
	}
	return ""
}
