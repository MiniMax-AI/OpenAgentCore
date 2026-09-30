package execution

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

// ErrModelProviderRequired reports a hosted or self-hosted Session that has no
// frozen model provider and therefore cannot run.
var ErrModelProviderRequired = errors.New("the Session has no model provider")

func (d *Dispatcher) executionRequest(ctx context.Context, session store.Session, snapshot Snapshot, caps runtimedevice.KindCapabilities, bound store.SessionExecutionBinding) (proto.PromptRequestPayload, error) {
	recoverNativeSession := bound.HasStartedTurn && bound.NativeSessionID == ""
	if recoverNativeSession && !caps.NativeSessionRecovery {
		return proto.PromptRequestPayload{}, errors.New("native session recovery is unavailable")
	}
	tools, err := executionTools(snapshot.Agent.Tools)
	if err != nil {
		return proto.PromptRequestPayload{}, err
	}
	options := map[string]any{}
	if snapshot.ModelProviderConfigured {
		options, err = d.sessionModelOptions(ctx, session)
		if err != nil {
			return proto.PromptRequestPayload{}, err
		}
	} else if snapshot.Environment != nil && v1.ModelProviderRequired(snapshot.Environment.Type) {
		// Require the frozen bundle before dispatch so the harness cannot
		// select an implicit provider endpoint.
		return proto.PromptRequestPayload{}, ErrModelProviderRequired
	}
	options["model"], options["system_prompt"] = snapshot.Agent.Model, snapshot.Agent.Instructions
	if snapshot.Agent.XAgentsCore != nil && len(snapshot.Agent.XAgentsCore.HarnessConfig) > 0 {
		var native map[string]any
		if err := json.Unmarshal(snapshot.Agent.XAgentsCore.HarnessConfig, &native); err != nil {
			return proto.PromptRequestPayload{}, err
		}
		options["harness_config"] = native
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
		AgentOptions: options, ExecutionControls: controls, AgentStateKey: "agents-api-" + session.ID,
		AgentSessionID: bound.NativeSessionID, StrictResume: true,
		RequireExistingNativeSession: recoverNativeSession,
		ObserveMessages:              caps.MessageItems, ObserveToolObservations: true,
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
