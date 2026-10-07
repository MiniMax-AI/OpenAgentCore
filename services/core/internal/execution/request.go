package execution

import (
	"context"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

// ErrModelProviderRequired reports a hosted or self-hosted Session that has no
// frozen model provider and therefore cannot run.
var ErrModelProviderRequired = errors.New("the Session has no model provider")

func (d *Dispatcher) executionRequest(ctx context.Context, session sessions.Session, snapshot Snapshot, caps runtimedevice.KindCapabilities, bound sessions.ExecutionBinding) (proto.PromptRequestPayload, error) {
	recoverNativeSession := bound.HasStartedTurn && bound.NativeSessionID == ""
	if recoverNativeSession && !caps.NativeSessionRecovery {
		return proto.PromptRequestPayload{}, errors.New("native session recovery is unavailable")
	}
	tools, err := executionTools(snapshot.Agent.Tools)
	if err != nil {
		return proto.PromptRequestPayload{}, err
	}
	var provider *modelprovider.Provider
	if snapshot.ModelProviderConfigured {
		provider, err = d.sessionModelProvider(ctx, session)
		if err != nil {
			return proto.PromptRequestPayload{}, err
		}
	} else if snapshot.Environment != nil && v1.ModelProviderRequired(snapshot.Environment.Type) {
		// Require the frozen bundle before dispatch so the harness cannot
		// select an implicit provider endpoint.
		return proto.PromptRequestPayload{}, ErrModelProviderRequired
	}
	var harnessConfig proto.HarnessConfig
	if snapshot.Agent.XAgentsCore != nil {
		harnessConfig = snapshot.Agent.XAgentsCore.HarnessConfig
	}
	instructions := ""
	if snapshot.Agent.Instructions != nil {
		instructions = *snapshot.Agent.Instructions
	}
	verbosity := snapshot.Agent.Text.Verbosity
	if verbosity == "" {
		verbosity = "medium"
	}
	controls := &proto.ExecutionControls{DisableProgrammaticToolCalling: tools.DisableProgrammatic, WebSearch: "disabled", TextVerbosity: verbosity}
	if snapshot.Agent.Text.Format.Type == "json_schema" {
		controls.OutputFormat = &proto.OutputFormat{Type: "json_schema", Schema: snapshot.Agent.Text.Format.Schema}
	}
	request := proto.PromptRequestPayload{AgentKind: session.Engine, FunctionTools: tools.Functions, ToolSearch: tools.Search,
		Model: snapshot.Agent.Model, SystemPrompt: instructions, ModelProvider: provider, HarnessConfig: harnessConfig,
		ExecutionControls: controls, AgentStateKey: "agents-api-" + session.ID,
		AgentSessionID: bound.NativeSessionID, RequireExistingNativeSession: recoverNativeSession,
		ObserveMessages:           caps.MessageItems,
		ObserveSubagentIdentities: snapshot.Agent.MultiAgent.Enabled,
		MaxConcurrentSubagents:    snapshot.Agent.MultiAgent.MaxConcurrentSubagents,
		DisableSubagents:          !snapshot.Agent.MultiAgent.Enabled}
	if len(tools.MCP) != 0 {
		selected, err := d.mcpExecutionCredentials(session.Engine, snapshot, tools.MCP, caps)
		if err != nil {
			return proto.PromptRequestPayload{}, err
		}
		for i := range tools.MCP {
			if binding, ok := selected[tools.MCP[i].ServerLabel]; ok {
				token, err := d.Credentials.MCPBearerToken(ctx, vaults.MCPBearerToken{TenantID: session.TenantID, VaultIDs: snapshot.VaultIDs, Binding: binding})
				if err != nil {
					return proto.PromptRequestPayload{}, err
				}
				tools.MCP[i].BearerToken = &token
			}
		}
		request.MCPHTTPServers = &tools.MCP
	}
	return request, nil
}
