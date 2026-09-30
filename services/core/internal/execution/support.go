package execution

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// ValidateSessionConfiguration checks engine placement and configuration before persistence.
func (p Policy) ValidateSessionConfiguration(engine string, configuration json.RawMessage) error {
	var snapshot Snapshot
	if json.Unmarshal(configuration, &snapshot) != nil {
		return store.ErrInvalidInput
	}
	profile, ok := p.Engines.Lookup(engine)
	if !ok || (snapshot.Environment != nil && !profile.Accepts(snapshot.Environment.Type)) {
		return store.ErrInvalidInput
	}
	_, err := p.mcpCredentialBindings(engine, snapshot)
	if err != nil {
		return err
	}
	if snapshot.Environment != nil && snapshot.Environment.Type == "self_hosted" {
		if strings.TrimSpace(snapshot.Agent.Model) == "" || !validSelfHostedPlacement(environmentPlacement{WorkspaceDirectory: snapshot.Environment.WorkspaceDirectory, CapabilityDirectories: snapshot.Environment.CapabilityDirectories}) {
			return store.ErrInvalidInput
		}
	}
	if snapshot.Environment != nil && snapshot.Environment.Type == "openai_hosted" {
		// Only this placement/engine combination has current native qualification.
		// Runtime capability checks still apply before any execution claim.
		if strings.TrimSpace(snapshot.Agent.Model) == "" {
			return store.ErrInvalidInput
		}
		configuration, err := json.Marshal(snapshot.Environment)
		if err != nil || !LocalWorkspaceConfiguration(configuration) {
			return store.ErrInvalidInput
		}
	}
	if snapshot.Environment != nil && snapshot.Environment.Type != "none" {
		// Current Runtime adapters cannot execute a restricted network policy.
		// Admission must reject it before allocating compute. Runtime itself is
		// not an enforcement boundary; new support needs outer qualification.
		if network := snapshot.Environment.Network; network != nil && network.Access != "enabled" {
			return errors.New("This execution environment does not support disabled or restricted networking. Use enabled networking; Runtime does not provide network isolation.")
		}
	}
	return validateProfileConfiguration(profile, snapshot)
}

func (p Policy) canAdmitInputs(engine string, configuration json.RawMessage) bool {
	var snapshot Snapshot
	if json.Unmarshal(configuration, &snapshot) != nil || !environmentNone(snapshot) {
		return false
	}
	return p.ValidateSessionConfiguration(engine, configuration) == nil
}

func (p Policy) validateEngineInputs(engine string, configuration json.RawMessage, inputs []store.Input) error {
	profile, ok := p.Engines.Lookup(engine)
	if !ok {
		return store.ErrInvalidInput
	}
	var snapshot Snapshot
	if json.Unmarshal(configuration, &snapshot) != nil {
		return store.ErrInvalidInput
	}
	placement := ""
	if snapshot.Environment != nil {
		placement = snapshot.Environment.Type
	}
	return validateProfileInputs(profile, placement, inputs)
}

// engineCapabilities is shared by device selection and the final preclaim check.
// Capability bits describe the adapter; supported values still depend on its profile.
func (p Policy) engineCapabilities(peer *runtimegateway.Session, engine string, snapshot Snapshot) (runtimedevice.KindCapabilities, error) {
	fail := func(message string) (runtimedevice.KindCapabilities, error) {
		return runtimedevice.KindCapabilities{}, errors.New(message)
	}
	profile, ok := p.Engines.Lookup(engine)
	if !ok || (snapshot.Environment != nil && !profile.Accepts(snapshot.Environment.Type)) {
		return fail("execution engine placement is not supported")
	}
	if err := validateProfileConfiguration(profile, snapshot); err != nil {
		return runtimedevice.KindCapabilities{}, err
	}
	info, found, known := peer.AgentKindStatus(engine)
	caps := info.Capabilities
	if !known || !found || !info.Available || !caps.Streaming || !caps.Steering || !caps.DurableTurns || !caps.DurableInputReceipts {
		return fail("device must advertise streaming, steering and durable turns for this engine")
	}
	if !caps.Preparation {
		return fail("device must advertise executor preparation")
	}
	if !caps.ExecutionControls {
		return fail("device must advertise execution_controls")
	}
	if profile.WebSearchControl.IsSupported() && !caps.WebSearchControl {
		return fail("device must advertise web_search_control")
	}
	if snapshot.Agent.Text.Format.Type == "json_schema" && (!caps.StructuredOutput || !caps.MessageItems) {
		return fail("device must support structured output and message observations")
	}
	if profile.TextVerbosity.IsSupported() && !caps.TextVerbosity {
		return fail("device must advertise text_verbosity")
	}
	if !caps.ToolObservations {
		return fail("device must advertise tool_observations")
	}
	if snapshot.Agent.MultiAgent.Enabled && !caps.SubagentObservations {
		return fail("device must support durable subagent observations")
	}
	if !snapshot.Agent.MultiAgent.Enabled && !caps.SubagentControl {
		return fail("device must advertise subagent_control")
	}
	tools, err := executionTools(snapshot.Agent.Tools)
	if err != nil {
		return fail("invalid execution tool configuration")
	}
	if err := (proto.PromptRequestPayload{ToolSearch: tools.Search, FunctionTools: tools.Functions}).ValidateToolSearch(caps.ToolSearch); err != nil {
		return fail(err.Error())
	}
	if tools.DisableProgrammatic && !caps.ProgrammaticToolCallingDisable {
		return fail("device must support disabling programmatic tool calling")
	}
	if len(tools.Functions) > 0 && !caps.FunctionTools {
		return fail("device must advertise function_tools")
	}
	if _, err := p.mcpExecutionCredentials(engine, snapshot, tools.MCP, caps); err != nil {
		return runtimedevice.KindCapabilities{}, err
	}
	if snapshot.Environment != nil && (snapshot.Environment.Type == "openai_hosted" || snapshot.Environment.Type == "self_hosted") {
		if !caps.Preparation || !caps.LocalEnvironment || !caps.WorkspaceReadPreparation || !caps.WorkspaceOutputExport {
			return fail("device must advertise local preparation, workspace reads and output export")
		}
	}
	if environmentNone(snapshot) && !caps.EnvironmentNone {
		return fail("device must advertise environment_none")
	}
	return caps, nil
}
