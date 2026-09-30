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
	var local struct {
		Plugins               []agentplugin.Metadata           `json:"plugins,omitempty"`
		Skills                []store.EnvironmentSkillMetadata `json:"skills,omitempty"`
		Files                 []store.InitialFileMetadata      `json:"files"`
		Packages              *v1.EnvironmentPackages          `json:"packages,omitempty"`
		Initialization        bool                             `json:"initialization,omitempty"`
		Type                  string                           `json:"type"`
		WorkspaceDirectory    string                           `json:"workspace_directory,omitempty"`
		CapabilityDirectories []string                         `json:"capability_directories"`
		Network               *v1.EnvironmentNetworkInput      `json:"network"`
	}
	decoder := json.NewDecoder(bytes.NewReader(configuration))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&local) != nil || agentcapabilities.ValidateSourceDirectories(local.CapabilityDirectories) != nil {
		return placement, store.ErrInvalidInput
	}
	// Placement selects a workspace; all preparation fields are shared.
	switch placement.Type {
	case "openai_hosted":
		if local.WorkspaceDirectory != "" || agentcapabilities.ValidateDirectories(local.CapabilityDirectories) != nil {
			return placement, store.ErrInvalidInput
		}
		placement.WorkspaceDirectory = "/workspace"
	case "self_hosted":
		if !validSelfHostedPlacement(placement) {
			return placement, store.ErrInvalidInput
		}
	default:
		return placement, store.ErrInvalidInput
	}
	placement.NetworkAccess = "enabled"
	if local.Network != nil {
		if (agentnetwork.Policy{Access: local.Network.Access, AllowedDomains: local.Network.AllowedDomains}).Validate() != nil {
			return placement, store.ErrInvalidInput
		}
		placement.NetworkAccess, placement.AllowedDomains = local.Network.Access, append([]string(nil), local.Network.AllowedDomains...)
	}
	return placement, nil
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
	sources := &agentcapabilities.Input{Plugins: append([]agentplugin.Metadata(nil), placement.Plugins...), Directories: append([]string(nil), placement.CapabilityDirectories...)}
	for _, metadata := range placement.Skills {
		if store.ValidateInstalledSkillMetadata(metadata) != nil {
			return store.ErrInvalidInput
		}
		sources.Skills = append(sources.Skills, (store.EnvironmentSkill{Metadata: metadata}).InstallationMetadata())
	}
	req.LocalEnvironment = &proto.LocalEnvironment{
		ID: environment.ID, WorkspaceDirectory: placement.WorkspaceDirectory,
		CapabilitySources: sources,
		Capabilities:      len(sources.Skills)+len(sources.Plugins)+len(sources.Directories) > 0,
		ToolEnvironment:   placement.ToolEnvironment,
	}
	req.LocalEnvironment.NetworkAccess = placement.NetworkAccess
	req.LocalEnvironment.AllowedDomains = append([]string(nil), placement.AllowedDomains...)
	return nil
}

func validSelfHostedPlacement(placement environmentPlacement) bool {
	return agentcapabilities.ValidateSourceDirectories([]string{placement.WorkspaceDirectory}) == nil &&
		agentcapabilities.ValidateSourceDirectories(placement.CapabilityDirectories) == nil
}
