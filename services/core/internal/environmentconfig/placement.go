package environmentconfig

import (
	"bytes"
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentnetwork"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
)

// Placement is the frozen workspace and preparation selection of an Environment.
type Placement struct {
	Plugins               []agentplugin.Metadata `json:"plugins,omitempty"`
	Skills                []SkillMetadata        `json:"skills,omitempty"`
	Type                  string                 `json:"type"`
	ToolEnvironment       bool                   `json:"initialization,omitempty"`
	NetworkAccess         string                 `json:"-"`
	AllowedDomains        []string               `json:"-"`
	WorkspaceDirectory    string                 `json:"workspace_directory"`
	CapabilityDirectories []string               `json:"capability_directories"`
}

// ParsePlacement validates stored Environment configuration without accessing its workspace.
func ParsePlacement(configuration json.RawMessage) (Placement, error) {
	var placement Placement
	if json.Unmarshal(configuration, &placement) != nil {
		return placement, ErrInvalid
	}
	var local struct {
		Plugins               []agentplugin.Metadata      `json:"plugins,omitempty"`
		Skills                []SkillMetadata             `json:"skills,omitempty"`
		Files                 []InitialFileMetadata       `json:"files"`
		Packages              *v1.EnvironmentPackages     `json:"packages,omitempty"`
		Initialization        bool                        `json:"initialization,omitempty"`
		Type                  string                      `json:"type"`
		WorkspaceDirectory    string                      `json:"workspace_directory,omitempty"`
		CapabilityDirectories []string                    `json:"capability_directories"`
		Network               *v1.EnvironmentNetworkInput `json:"network"`
	}
	decoder := json.NewDecoder(bytes.NewReader(configuration))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&local) != nil || agentcapabilities.ValidateSourceDirectories(local.CapabilityDirectories) != nil {
		return placement, ErrInvalid
	}
	// Placement selects a workspace; all preparation fields are shared.
	switch placement.Type {
	case "openai_hosted":
		if local.WorkspaceDirectory != "" || agentcapabilities.ValidateDirectories(local.CapabilityDirectories) != nil {
			return placement, ErrInvalid
		}
		placement.WorkspaceDirectory = "/workspace"
	case "self_hosted":
		if !ValidSelfHostedPlacement(placement) {
			return placement, ErrInvalid
		}
	default:
		return placement, ErrInvalid
	}
	placement.NetworkAccess = "enabled"
	if local.Network != nil {
		if (agentnetwork.Policy{Access: local.Network.Access, AllowedDomains: local.Network.AllowedDomains}).Validate() != nil {
			return placement, ErrInvalid
		}
		placement.NetworkAccess, placement.AllowedDomains = local.Network.Access, append([]string(nil), local.Network.AllowedDomains...)
	}
	return placement, nil
}

// ValidSelfHostedPlacement checks the path restrictions shared by admission and stored configuration.
func ValidSelfHostedPlacement(placement Placement) bool {
	return agentcapabilities.ValidateSourceDirectories([]string{placement.WorkspaceDirectory}) == nil &&
		agentcapabilities.ValidateSourceDirectories(placement.CapabilityDirectories) == nil
}
