package execution

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// ValidateSessionConfiguration checks engine placement and configuration before persistence.
func (p Policy) ValidateSessionConfiguration(engine string, configuration json.RawMessage) error {
	var snapshot Snapshot
	if json.Unmarshal(configuration, &snapshot) != nil {
		return sessions.ErrInvalidInput
	}
	declaration, ok := p.Harnesses().Lookup(engine)
	if !ok || !declaration.Accepts(snapshot.Environment.Type) {
		return sessions.ErrInvalidInput
	}
	_, err := p.mcpCredentialBindings(engine, snapshot)
	if err != nil {
		return err
	}
	if snapshot.Environment.Type == "self_hosted" && !validSelfHostedPlacement(environmentPlacement{WorkspaceDirectory: snapshot.Environment.WorkspaceDirectory, CapabilityDirectories: snapshot.Environment.CapabilityDirectories}) {
		return sessions.ErrInvalidInput
	}
	if snapshot.Environment.Type == "openai_hosted" {
		configuration, err := json.Marshal(snapshot.Environment)
		if err != nil || !LocalWorkspaceConfiguration(configuration) {
			return sessions.ErrInvalidInput
		}
	}
	if snapshot.Environment.Type != "none" {
		// Current Runtime adapters cannot execute a restricted network policy.
		// Admission must reject it before allocating compute. Runtime itself is
		// not an enforcement boundary; new support needs outer isolation.
		if network := snapshot.Environment.Network; network != nil && network.Access != "enabled" {
			return errors.New("This execution environment does not support disabled or restricted networking. Use enabled networking; Runtime does not provide network isolation.")
		}
	}
	return validateDeclaredConfiguration(declaration, snapshot)
}

func (p Policy) canAdmitInputs(engine string, configuration json.RawMessage) bool {
	var snapshot Snapshot
	if json.Unmarshal(configuration, &snapshot) != nil || !environmentNone(snapshot) {
		return false
	}
	return p.ValidateSessionConfiguration(engine, configuration) == nil
}

func (p Policy) validateEngineInputs(engine string, configuration json.RawMessage, inputs []sessions.Input) error {
	declaration, ok := p.Harnesses().Lookup(engine)
	if !ok {
		return sessions.ErrInvalidInput
	}
	var snapshot Snapshot
	if json.Unmarshal(configuration, &snapshot) != nil {
		return sessions.ErrInvalidInput
	}
	return validateDeclaredInputs(declaration, inputs)
}

// engineCapabilities is shared by device selection and the final preclaim check.
// Capability bits describe the connected adapter; the declaration states what it may be asked to do.
func (p Policy) engineCapabilities(peer *runtimegateway.Session, engine string, snapshot Snapshot) (proto.AgentKindCapabilities, error) {
	fail := func(message string) (proto.AgentKindCapabilities, error) {
		return proto.AgentKindCapabilities{}, errors.New(message)
	}
	declaration, ok := p.Harnesses().Lookup(engine)
	if !ok || !declaration.Accepts(snapshot.Environment.Type) {
		return fail("execution engine placement is not supported")
	}
	if err := validateDeclaredConfiguration(declaration, snapshot); err != nil {
		return proto.AgentKindCapabilities{}, err
	}
	info, found, known := peer.AgentKindStatus(engine)
	caps := info.Capabilities
	if !known || !found || !info.Available || !caps.Streaming.IsSupported() || !caps.DurableTurns.IsSupported() || !caps.DurableInputReceipts.IsSupported() {
		return fail("device must advertise streaming, durable input receipts and durable turns for this engine")
	}
	if !caps.Preparation.IsSupported() {
		return fail("device must advertise executor preparation")
	}
	if !caps.ExecutionControls.IsSupported() {
		return fail("device must advertise execution_controls")
	}
	if snapshot.Agent.Text.Format.Type == "json_schema" && (!caps.StructuredOutput.IsSupported() || !caps.MessageItems.IsSupported()) {
		return fail("device must support structured output and message observations")
	}
	if !caps.ToolObservations.IsSupported() {
		return fail("device must advertise tool_observations")
	}
	if snapshot.Agent.MultiAgent.Enabled && !caps.SubagentObservations.IsSupported() {
		return fail("device must support durable subagent observations")
	}
	if !snapshot.Agent.MultiAgent.Enabled && !caps.SubagentControl.IsSupported() {
		return fail("device must advertise subagent_control")
	}
	tools, err := executionTools(snapshot.Agent.Tools)
	if err != nil {
		return fail("invalid execution tool configuration")
	}
	if err := (proto.PromptRequestPayload{ToolSearch: tools.Search, FunctionTools: tools.Functions}).ValidateToolSearch(caps.ToolSearch.IsSupported()); err != nil {
		return fail(err.Error())
	}
	if len(tools.Functions) > 0 && !caps.FunctionTools.IsSupported() {
		return fail("device must advertise function_tools")
	}
	if _, err := p.mcpExecutionCredentials(engine, snapshot, tools.MCP, caps); err != nil {
		return proto.AgentKindCapabilities{}, err
	}
	if snapshot.Environment.Type == "openai_hosted" || snapshot.Environment.Type == "self_hosted" {
		if !caps.Preparation.IsSupported() || !caps.LocalEnvironment.IsSupported() || !caps.WorkspaceReadPreparation.IsSupported() || !caps.WorkspaceOutputExport.IsSupported() {
			return fail("device must advertise local preparation, workspace reads and output export")
		}
	}
	if environmentNone(snapshot) && !caps.EnvironmentNone.IsSupported() {
		return fail("device must advertise environment_none")
	}
	return caps, nil
}

// sessionCapabilities shares storage-specific validation between device selection
// and the final preparation gate. The immutable binding, not today's deployment,
// determines whether this Session requires native history outside compute.
func (d *Dispatcher) sessionCapabilities(ctx context.Context, peer *runtimegateway.Session, session sessions.Session, snapshot Snapshot) (proto.AgentKindCapabilities, error) {
	caps, err := d.engineCapabilities(peer, session.Engine, snapshot)
	if err != nil {
		return caps, err
	}
	if snapshot.Environment.Type != "openai_hosted" {
		return caps, nil
	}
	environment, err := d.SessionsReader.GetSessionEnvironment(ctx, session.TenantID, session.ID)
	if err != nil {
		return proto.AgentKindCapabilities{}, err
	}
	if err := d.validateEnvironmentHistory(session.Engine, environment, caps.RetainedNativeHistory.IsSupported()); err != nil {
		return proto.AgentKindCapabilities{}, err
	}
	return caps, nil
}

func (p Policy) validateEnvironmentHistory(engine string, environment sessions.Environment, supported bool) error {
	declaration, found := p.Harnesses().Lookup(engine)
	if !found {
		return sessions.ErrInvalidInput
	}
	return sessions.ValidateRetainedHistory(environment.ExternalWorkspace, supported && declaration.Capabilities.RetainedNativeHistory.IsSupported())
}
