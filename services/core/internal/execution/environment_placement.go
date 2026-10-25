package execution

import (
	"bytes"
	"encoding/json"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimebootstrap"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

type environmentPlacement struct {
	Plugins               []proto.PluginMetadata            `json:"plugins,omitempty"`
	Skills                []environmentconfig.SkillMetadata `json:"skills,omitempty"`
	Type                  string                            `json:"type"`
	ToolEnvironment       bool                              `json:"initialization,omitempty"`
	WorkspaceDirectory    string                            `json:"workspace_directory"`
	CapabilityDirectories []string                          `json:"capability_directories"`
}

// LocalWorkspaceConfiguration recognizes a stored V1 profile. It
// does not provision a Runtime, validate live authority, or admit hosted creation.
func LocalWorkspaceConfiguration(configuration json.RawMessage) bool {
	placement, err := parseEnvironmentPlacement(configuration)
	return err == nil && (placement.Type == "openai_hosted" || placement.Type == "self_hosted")
}

func parseEnvironmentPlacement(configuration json.RawMessage) (environmentPlacement, error) {
	var placement environmentPlacement
	if json.Unmarshal(configuration, &placement) != nil {
		return placement, sessions.ErrInvalidInput
	}
	var local struct {
		Plugins               []proto.PluginMetadata                  `json:"plugins,omitempty"`
		Skills                []environmentconfig.SkillMetadata       `json:"skills,omitempty"`
		Files                 []environmentconfig.InitialFileMetadata `json:"files"`
		Packages              *v1.EnvironmentPackages                 `json:"packages,omitempty"`
		Initialization        bool                                    `json:"initialization,omitempty"`
		Type                  string                                  `json:"type"`
		WorkspaceDirectory    string                                  `json:"workspace_directory,omitempty"`
		CapabilityDirectories []string                                `json:"capability_directories"`
		Network               *v1.EnvironmentNetworkInput             `json:"network"`
	}
	decoder := json.NewDecoder(bytes.NewReader(configuration))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&local) != nil || proto.ValidateSourceDirectories(local.CapabilityDirectories) != nil {
		return placement, sessions.ErrInvalidInput
	}
	// Placement selects a workspace; all preparation fields are shared.
	switch placement.Type {
	case "openai_hosted":
		if local.WorkspaceDirectory != "" || agentcapabilities.ValidateDirectories(local.CapabilityDirectories) != nil {
			return placement, sessions.ErrInvalidInput
		}
		placement.WorkspaceDirectory = runtimebootstrap.PublicWorkspace
	case "self_hosted":
		if !validSelfHostedPlacement(placement) {
			return placement, sessions.ErrInvalidInput
		}
	default:
		return placement, sessions.ErrInvalidInput
	}
	return placement, nil
}

func environmentDeviceMatches(session sessions.Session, environment sessions.Environment, bound sessions.ExecutionDevice) bool {
	if environment.SessionID != session.ID || environment.TenantID != session.TenantID {
		return false
	}
	return bound.EnvironmentID == environment.ID
}

func (d *Dispatcher) configurePreparedEnvironment(session sessions.Session, environment sessions.Environment, bound sessions.ExecutionDevice, req *proto.PromptRequestPayload) error {
	placement, err := parseEnvironmentPlacement(environment.Configuration)
	if err != nil || !environmentDeviceMatches(session, environment, bound) {
		return sessions.ErrInvalidInput
	}
	sources := &proto.CapabilitySources{Plugins: append([]proto.PluginMetadata(nil), placement.Plugins...), Directories: append([]string(nil), placement.CapabilityDirectories...)}
	for _, metadata := range placement.Skills {
		if metadata.ValidateInstalled() != nil {
			return sessions.ErrInvalidInput
		}
		sources.Skills = append(sources.Skills, (environmentconfig.Skill{Metadata: metadata}).InstallationMetadata())
	}
	req.LocalEnvironment = &proto.LocalEnvironment{
		ID: environment.ID, WorkspaceDirectory: placement.WorkspaceDirectory,
		CapabilitySources: sources,
		Capabilities:      len(sources.Skills)+len(sources.Plugins)+len(sources.Directories) > 0,
		ToolEnvironment:   placement.ToolEnvironment,
	}
	return nil
}

func validSelfHostedPlacement(placement environmentPlacement) bool {
	return proto.ValidateSourceDirectories([]string{placement.WorkspaceDirectory}) == nil &&
		proto.ValidateSourceDirectories(placement.CapabilityDirectories) == nil
}
