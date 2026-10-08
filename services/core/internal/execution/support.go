package execution

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// ValidateSessionConfiguration checks a Session's frozen configuration before
// persistence: the common placement rules, then the Harness's static
// declaration.
func ValidateSessionConfiguration(engine string, configuration json.RawMessage) error {
	var snapshot Snapshot
	if json.Unmarshal(configuration, &snapshot) != nil || snapshot.Environment == nil {
		return sessions.ErrInvalidInput
	}
	static, ok := builtin.Registry().Lookup(engine)
	if !ok {
		return sessions.ErrInvalidInput
	}
	switch snapshot.Environment.Type {
	case "none":
	case "self_hosted":
		if strings.TrimSpace(snapshot.Agent.Model) == "" || !validSelfHostedPlacement(environmentPlacement{WorkspaceDirectory: snapshot.Environment.WorkspaceDirectory, CapabilityDirectories: snapshot.Environment.CapabilityDirectories}) {
			return sessions.ErrInvalidInput
		}
	case "openai_hosted":
		configuration, err := json.Marshal(snapshot.Environment)
		if strings.TrimSpace(snapshot.Agent.Model) == "" || err != nil || !LocalWorkspaceConfiguration(configuration) {
			return sessions.ErrInvalidInput
		}
	default:
		return sessions.ErrInvalidInput
	}
	if snapshot.Environment.Type != "none" {
		// Current Runtime adapters cannot execute a restricted network policy.
		// Admission must reject it before allocating compute. Runtime itself is
		// not an enforcement boundary; new support needs outer qualification.
		if network := snapshot.Environment.Network; network != nil && network.Access != "enabled" {
			return errors.New("This execution environment does not support disabled or restricted networking. Use enabled networking; Runtime does not provide network isolation.")
		}
	}
	if _, err := executionTools(snapshot.Agent.Tools); err != nil {
		return err
	}
	selection, err := harnessSelection(snapshot)
	if err != nil {
		return err
	}
	return proto.ValidateSelection(static.Declaration, selection)
}

// harnessSelection projects the frozen Session configuration. A server with a
// frozen credential binding is authenticated.
func harnessSelection(snapshot Snapshot) (proto.Selection, error) {
	selection := agents.HarnessSelection(snapshot.Agent, snapshot.Environment)
	selected, err := selectedMCPCredentials(snapshot)
	if err != nil {
		return proto.Selection{}, err
	}
	for i := range selection.MCP {
		_, selection.MCP[i].Bearer = selected[selection.MCP[i].Label]
	}
	return selection, nil
}

func canAdmitInputs(engine string, configuration json.RawMessage) bool {
	var snapshot Snapshot
	if json.Unmarshal(configuration, &snapshot) != nil || !environmentNone(snapshot) {
		return false
	}
	return ValidateSessionConfiguration(engine, configuration) == nil
}

// validateInputs checks messages and function results against the Harness's
// static declaration before any write, reservation or promotion.
func validateInputs(engine string, inputs []sessions.Input) error {
	static, ok := builtin.Registry().Lookup(engine)
	if !ok {
		return sessions.ErrInvalidInput
	}
	for _, input := range inputs {
		var selection proto.Selection
		switch input.Kind {
		case "message":
			messages, err := messageInput(input.Payload)
			if err != nil {
				return err
			}
			selection.Messages = messages
		case "tool_result":
			var value sessions.FunctionResultInput
			if json.Unmarshal(input.Payload, &value) != nil {
				return sessions.ErrInvalidInput
			}
			result, err := functionResult(sessions.FunctionCall{CallID: value.CallID, Result: value.Result})
			if err != nil {
				return sessions.ErrInvalidInput
			}
			selection.FunctionResult = &result
		default:
			continue
		}
		if err := proto.ValidateSelection(static.Declaration, selection); err != nil {
			return err
		}
	}
	return nil
}

// runtimeDeclaration is engine's static declaration narrowed to what the
// peer's heartbeat advertises.
func runtimeDeclaration(peer *runtimegateway.Session, engine string) (proto.Declaration, error) {
	static, ok := builtin.Registry().Lookup(engine)
	info, found, known := peer.AgentKindStatus(engine)
	if !ok || !known || !found || !info.Available {
		return proto.Declaration{}, errors.New("device must advertise this engine as available")
	}
	return static.Declaration.Narrow(info.Capabilities)
}

// admitSession checks the Session and the messages it is about to send
// against the peer's declaration, at device selection and the final preclaim
// check.
func admitSession(peer *runtimegateway.Session, engine string, snapshot Snapshot, messages proto.MessageInput) (proto.Declaration, error) {
	declaration, err := runtimeDeclaration(peer, engine)
	if err != nil {
		return proto.Declaration{}, err
	}
	selection, err := harnessSelection(snapshot)
	if err != nil {
		return proto.Declaration{}, err
	}
	selection.Messages = messages
	return declaration, proto.ValidateSelection(declaration, selection)
}

// validateDelivery checks a message or function result delivered to a running
// Turn against the peer's declaration.
func validateDelivery(peer *runtimegateway.Session, engine string, selection proto.Selection) error {
	declaration, err := runtimeDeclaration(peer, engine)
	if err != nil {
		return err
	}
	return proto.ValidateSelection(declaration, selection)
}
