package execution

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

func (d *Dispatcher) executionRequest(ctx context.Context, session sessions.Session, snapshot Snapshot, declaration proto.Declaration, bound sessions.ExecutionBinding) (proto.PromptRequestPayload, error) {
	recoverNativeSession := bound.HasStartedTurn && bound.NativeSessionID == ""
	if err := proto.ValidateSelection(declaration, proto.Selection{NativeSessionRecovery: recoverNativeSession}); err != nil {
		return proto.PromptRequestPayload{}, err
	}
	tools, err := executionTools(snapshot.Agent.Tools)
	if err != nil {
		return proto.PromptRequestPayload{}, err
	}
	provider, err := d.sessionModelProvider(ctx, session)
	if err != nil {
		return proto.PromptRequestPayload{}, err
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
	controls := &proto.ExecutionControls{DisableProgrammaticToolCalling: tools.DisableProgrammatic, TextVerbosity: verbosity}
	if snapshot.Agent.Text.Format.Type == "json_schema" {
		controls.OutputFormat = &proto.OutputFormat{Type: "json_schema", Schema: snapshot.Agent.Text.Format.Schema}
	}
	request := proto.PromptRequestPayload{AgentKind: session.Engine, FunctionTools: tools.Functions, ToolSearch: tools.Search,
		Model: snapshot.Agent.Model, SystemPrompt: instructions, ModelProvider: provider, HarnessConfig: harnessConfig,
		ExecutionControls: controls, AgentSessionID: bound.NativeSessionID, RequireExistingNativeSession: recoverNativeSession,
		ObserveSubagentIdentities: snapshot.Agent.MultiAgent.Enabled,
		MaxConcurrentSubagents:    snapshot.Agent.MultiAgent.MaxConcurrentSubagents,
		DisableSubagents:          !snapshot.Agent.MultiAgent.Enabled}
	if len(tools.MCP) != 0 {
		selected, err := selectedMCPCredentials(snapshot)
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
