package execution

import (
	"encoding/json"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// LocalWorkspaceConfiguration recognizes the qualified stored V1 profile. It
// does not provision a Runtime, validate live authority, or admit hosted creation.
func LocalWorkspaceConfiguration(configuration json.RawMessage) bool {
	placement, err := environmentconfig.ParsePlacement(configuration)
	return err == nil && (placement.Type == "openai_hosted" || placement.Type == "self_hosted")
}

func (d *Dispatcher) configurePreparedEnvironment(session sessions.Session, environment sessions.Environment, bound sessions.ExecutionDevice, req *proto.PromptRequestPayload) error {
	placement, err := environmentconfig.ParsePlacement(environment.Configuration)
	if err != nil || environment.SessionID != session.ID || environment.TenantID != session.TenantID || bound.SessionEnvironmentID != environment.ID {
		return sessions.ErrInvalidInput
	}
	sources := &agentcapabilities.Input{Plugins: append([]agentplugin.Metadata(nil), placement.Plugins...), Directories: append([]string(nil), placement.CapabilityDirectories...)}
	for _, metadata := range placement.Skills {
		if metadata.ValidateInstalled() != nil {
			return sessions.ErrInvalidInput
		}
		sources.Skills = append(sources.Skills, (environmentconfig.Skill{Metadata: metadata}).InstallationMetadata())
	}
	req.LocalEnvironment = &proto.LocalEnvironment{ID: environment.ID, CapabilitySources: sources, ToolEnvironment: placement.ToolEnvironment}
	return nil
}
