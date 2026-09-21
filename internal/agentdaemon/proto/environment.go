package proto

import (
	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentplugin"
)

// LocalEnvironment references a deployment-bound workspace; it never supplies a path.
type LocalEnvironment struct {
	ID string `json:"id"`
	// Capabilities requests the completed, protected Runtime installation.
	Capabilities bool `json:"capabilities,omitempty"`
	// Skills is resolved by the bound daemon; wire input cannot supply paths.
	Skills []agentcapabilities.InstalledSkill `json:"-"`
	// MCP is resolved from the same protected installation, never from wire input.
	MCP []EnvironmentMCP `json:"-"`
	// ToolEnvironment consumes Core-completed confidential initialization.
	ToolEnvironment bool `json:"tool_environment,omitempty"`
	// SystemPackages requires the installed Runtime tool root during execution.
	SystemPackages bool `json:"system_packages,omitempty"`
	// NetworkAccess must match the immutable Runtime policy for execution.
	NetworkAccess  string   `json:"network_access,omitempty"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
}

// EnvironmentMCP is transient Runtime configuration. Do not log it: HTTP headers
// and the selected user bearer may be confidential. It is not agent.tools MCP.
type EnvironmentMCP struct {
	PackageRoot string
	Server      agentplugin.MCPServer
	BearerToken *string
}

func (r PromptRequestPayload) EnvironmentID() string {
	if r.LocalEnvironment != nil {
		return r.LocalEnvironment.ID
	}
	return ""
}
