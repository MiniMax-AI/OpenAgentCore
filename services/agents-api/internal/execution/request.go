package execution

import (
	"context"
	"errors"
	"maps"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func (d *Dispatcher) executionRequest(ctx context.Context, session store.Session, snapshot Snapshot, caps device.KindCapabilities, bound store.SessionExecutionBinding) (proto.PromptRequestPayload, error) {
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
		options, err = d.sessionModelOptions(ctx, session, snapshot.Agent.Model)
		if err != nil {
			return proto.PromptRequestPayload{}, err
		}
	} else if snapshot.Environment != nil && v1.ModelProviderRequired(snapshot.Environment.Type) {
		// A Session from before deployment defaults moved into Core. Without a
		// bundle its harness would fall back to a built-in endpoint and hang.
		return proto.PromptRequestPayload{}, store.ErrModelProviderRequired
	} else if d.Options != nil {
		options, err = d.Options(ctx, session)
		if err != nil {
			return proto.PromptRequestPayload{}, err
		}
		options = maps.Clone(options)
		if options == nil {
			options = map[string]any{}
		}
	}
	options["model"], options["system_prompt"] = snapshot.Agent.Model, snapshot.Agent.Instructions
	delete(options, "override_system_prompt")
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
		AgentSessionID: bound.NativeSessionID, ReleaseOnCompletion: true, StrictResume: true,
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
		if len(selected) > 0 && d.Store == nil {
			return proto.PromptRequestPayload{}, errors.New("authenticated MCP execution is unavailable")
		}
		for i := range tools.MCP {
			if binding, ok := selected[tools.MCP[i].ServerLabel]; ok {
				token, err := d.Store.MCPBearerToken(ctx, session.TenantID, snapshot.VaultIDs, binding)
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
