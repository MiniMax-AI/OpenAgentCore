package execution

import (
	"bytes"
	"encoding/json"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentnetwork"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentplugin"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type environmentPlacement struct {
	Plugins               []agentplugin.Metadata           `json:"plugins,omitempty"`
	Skills                []store.EnvironmentSkillMetadata `json:"skills,omitempty"`
	Type                  string                           `json:"type"`
	ToolEnvironment       bool                             `json:"initialization,omitempty"`
	SystemPackages        bool                             `json:"-"`
	NetworkAccess         string                           `json:"-"`
	AllowedDomains        []string                         `json:"-"`
	WorkspaceDirectory    string                           `json:"workspace_directory"`
	CapabilityDirectories []string                         `json:"capability_directories"`
}

// LocalWorkspaceConfiguration recognizes the qualified stored V1 profile. It
// does not provision a Runtime, validate live authority, or admit hosted creation.
func LocalWorkspaceConfiguration(configuration json.RawMessage) bool {
	placement, err := parseEnvironmentPlacement(configuration)
	return err == nil && (placement.Type == "openai_hosted" || placement.Type == "self_hosted")
}

func parseEnvironmentPlacement(configuration json.RawMessage) (environmentPlacement, error) {
	var placement environmentPlacement
	if json.Unmarshal(configuration, &placement) != nil {
		return placement, store.ErrInvalidInput
	}
	switch placement.Type {
	case "self_hosted":
		if placement.WorkspaceDirectory == "/workspace" && len(placement.CapabilityDirectories) == 0 {
			placement.NetworkAccess = "enabled"
			return placement, nil
		}
	case "openai_hosted":
		// Stored policy is shared by preparation and provider bootstrap.
		var local struct {
			Plugins               []agentplugin.Metadata           `json:"plugins,omitempty"`
			Skills                []store.EnvironmentSkillMetadata `json:"skills,omitempty"`
			Files                 []store.InitialFileMetadata      `json:"files"`
			Packages              *v1.EnvironmentPackages          `json:"packages,omitempty"`
			Initialization        bool                             `json:"initialization,omitempty"`
			Type                  string                           `json:"type"`
			CapabilityDirectories []string                         `json:"capability_directories"`
			Network               *struct {
				Access         string   `json:"access"`
				AllowedDomains []string `json:"allowed_domains"`
			} `json:"network"`
		}
		decoder := json.NewDecoder(bytes.NewReader(configuration))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&local) == nil && agentcapabilities.ValidateDirectories(local.CapabilityDirectories) == nil {
			placement.SystemPackages = local.Packages != nil && len(local.Packages.System) > 0
			placement.NetworkAccess = "enabled"
			if local.Network != nil {
				if (agentnetwork.Policy{Access: local.Network.Access, AllowedDomains: local.Network.AllowedDomains}).Validate() != nil {
					return placement, store.ErrInvalidInput
				}
				placement.NetworkAccess = local.Network.Access
				placement.AllowedDomains = append([]string(nil), local.Network.AllowedDomains...)
			}
			return placement, nil
		}
	}
	return placement, store.ErrInvalidInput
}

func environmentDeviceMatches(session store.Session, environment store.Environment, bound store.ExecutionDevice) bool {
	if environment.SessionID != session.ID || environment.TenantID != session.TenantID {
		return false
	}
	return bound.EnvironmentID == environment.ID
}

func (d *Dispatcher) configurePreparedEnvironment(session store.Session, environment store.Environment, bound store.ExecutionDevice, req *proto.PromptRequestPayload) error {
	placement, err := parseEnvironmentPlacement(environment.Configuration)
	if err != nil || !environmentDeviceMatches(session, environment, bound) {
		return store.ErrInvalidInput
	}
	if placement.SystemPackages && !placement.ToolEnvironment {
		return store.ErrInvalidInput
	}
	req.LocalEnvironment = &proto.LocalEnvironment{ID: environment.ID, Capabilities: len(placement.Plugins)+len(placement.CapabilityDirectories) > 0, ToolEnvironment: placement.ToolEnvironment, SystemPackages: placement.SystemPackages}
	for _, metadata := range placement.Skills {
		if store.ValidateInstalledSkillMetadata(metadata) != nil {
			return store.ErrInvalidInput
		}
		req.LocalEnvironment.Capabilities = true
	}
	req.LocalEnvironment.NetworkAccess = placement.NetworkAccess
	req.LocalEnvironment.AllowedDomains = append([]string(nil), placement.AllowedDomains...)
	return nil
}
