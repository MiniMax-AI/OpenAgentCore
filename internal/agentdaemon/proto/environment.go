package proto

import (
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
)

// LocalEnvironment names a frozen workspace selection. Runtime must verify it
// against the bound local root before resolving capabilities or native execution.
type LocalEnvironment struct {
	CapabilityRoot     string                   `json:"-"`
	ID                 string                   `json:"id"`
	WorkspaceDirectory string                   `json:"workspace_directory"`
	CapabilitySources  *agentcapabilities.Input `json:"capability_sources"`
	// WorkspaceRoot is the bound local root the daemon supplies for execution;
	// wire input cannot supply it. Read-only preparation leaves it empty.
	WorkspaceRoot string `json:"-"`
	// Capabilities is derived from the frozen selection for engine qualification;
	// Runtime still ensures and loads the protected installation before execution.
	Capabilities bool `json:"capabilities,omitempty"`
	// Skills is resolved by the bound daemon; wire input cannot supply paths.
	Skills []agentcapabilities.InstalledSkill `json:"-"`
	// MCP is resolved from the same protected installation, never from wire input.
	MCP []EnvironmentMCP `json:"-"`
	// ToolEnvironment consumes Core-completed confidential initialization.
	ToolEnvironment bool `json:"tool_environment,omitempty"`
	// NetworkAccess must match the immutable Runtime policy for execution.
	NetworkAccess  string   `json:"network_access,omitempty"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
}

// EnvironmentMCP is transient Runtime configuration. Do not log it: HTTP headers
// and the selected user bearer may be confidential. It is not agent.tools MCP.
type EnvironmentMCP struct {
	InstallationRoot string
	WorkspaceRoot    string
	PackageRoot      string
	Server           agentplugin.MCPServer
	BearerToken      *string
}

func (r PromptRequestPayload) EnvironmentID() string {
	if r.LocalEnvironment != nil {
		return r.LocalEnvironment.ID
	}
	return ""
}
