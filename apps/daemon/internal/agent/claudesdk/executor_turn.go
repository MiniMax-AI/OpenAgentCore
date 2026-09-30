package claudesdk

import (
	"encoding/json"
	"fmt"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"strings"
	"time"
)

func (s *session) runTurn(start startRequest, out chan<- proto.Envelope) {
	defer close(s.outputDone)
	defer s.owner.finishTurn(s)
	defer close(out)
	defer s.stopFunctions()
	defer s.stopSteering()
	var failure error
	outputLost := false
	terminalLost := false
	emit := func(kind string, payload any) {
		event, err := proto.NewEnvelope(kind, s.runID, payload)
		if err != nil {
			return
		}
		select {
		case out <- event:
			return
		default:
		}
		var cancelled <-chan struct{}
		if kind != proto.TypeDone && kind != proto.TypeError {
			cancelled = s.cancelOutput
		}
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case out <- event:
		case <-cancelled:
			outputLost = true
		case <-timer.C:
			outputLost = true
			if kind == proto.TypeDone || kind == proto.TypeError {
				terminalLost = true
			}
			s.invalidate()
		}
	}
	var content strings.Builder
	var result *bridgeEvent
	var classifiedFailure error
	var engineCode, failedResultID string
	var usage proto.Usage
	var usageSession string
	usageIDs := map[string]bool{}
	var sequence uint64
	terminal := false
	mcp := mcpState{calls: map[string]proto.ToolObservation{}}
	commands := commandState{calls: map[string]proto.ToolObservation{}}
	settlementReceived := false
	settlementConfirmed := false
	cancelled := false
	reusable := false
	reason := "bridge_interrupted"
	for raw := range s.frames {
		var event bridgeEvent
		if err := json.Unmarshal(raw, &event); err != nil || event.TurnID != s.runID {
			failure = fmt.Errorf("claudesdk: invalid Turn event identity")
			s.invalidate()
			break
		}
		if event.Type == "turn_started" && !terminal {
			continue
		}
		if event.Type == "turn_settled" {
			if !terminal || event.Reusable == nil || event.Confirmed == nil || (*event.Reusable && !*event.Confirmed) || (!*event.Reusable && event.Reason == "") {
				failure = fmt.Errorf("claudesdk: invalid Turn settlement")
				s.invalidate()
				break
			}
			settlementReceived, settlementConfirmed, reusable, reason = true, *event.Confirmed, *event.Reusable, event.Reason
			break
		}
		if terminal {
			failure = fmt.Errorf("claudesdk: invalid SDK bridge output")
			s.invalidate()
			break
		}
		switch event.Type {
		case "command_observation":
			if err := commands.receive(event, start, s.inputSessionID(), emit); err != nil {
				failure = err
				s.invalidate()
			}
		case "mcp_observation":
			if err := mcp.receive(event, start, emit); err != nil {
				failure = err
				s.invalidate()
			}
		case "delta":
			if start.ObserveMessages && event.ItemID == "" || !start.ObserveMessages && event.ItemID != "" {
				failure = fmt.Errorf("claudesdk: invalid message delta identity")
				s.invalidate()
				break
			}
			content.WriteString(event.Delta)
			sequence++
			emit(proto.TypeDelta, proto.DeltaPayload{ItemID: event.ItemID, Delta: event.Delta, Sequence: sequence})
		case "output_message":
			message := event.Message
			if !start.ObserveMessages || message == nil || message.ID == "" ||
				(message.Status != "in_progress" && message.Status != "completed") ||
				(message.Status == "completed") != (message.Text != nil) {
				failure = fmt.Errorf("claudesdk: invalid message observation")
				s.invalidate()
				break
			}
			emit(proto.TypeOutputMessage, message)
		case "function_call", "function_applied":
			if err := s.receiveFunction(event, start, emit); err != nil {
				failure = err
				s.invalidate()
			}
		case "input_ready", "input_closed", "input_applied", "input_rejected":
			if err := s.receiveInput(event, start); err != nil {
				failure = err
				s.invalidate()
			}
		case proto.TypeSubagentIdentity, proto.TypeSubagentTurn, proto.TypeSubagentItem, proto.TypeSubagentCoordination:
			if start.Subagents == nil || !json.Valid(event.Fact) {
				failure = fmt.Errorf("claudesdk: unrequested native child observation")
				s.invalidate()
				break
			}
			emit(event.Type, event.Fact)
		case "usage":
			if event.ResultID == "" || !s.matchesInputSession(event.SessionID) || event.SessionID == "" || (start.Resume != "" && event.SessionID != start.Resume) ||
				(usageSession != "" && usageSession != event.SessionID) || usageIDs[event.ResultID] {
				failure = fmt.Errorf("claudesdk: invalid usage identity or duplicate result")
				s.invalidate()
				break
			}
			nextUsage, err := appendNativeUsage(usage, event.Usage)
			failure = err
			if failure != nil {
				s.invalidate()
				break
			}
			usage = nextUsage
			usageIDs[event.ResultID] = true
			usageSession = event.SessionID
			failedResultID = ""
			var nativeResult struct {
				Subtype string `json:"subtype"`
				IsError bool   `json:"is_error"`
			}
			if json.Unmarshal(event.Usage, &nativeResult) == nil && (nativeResult.Subtype == "error_during_execution" || nativeResult.Subtype == "success" && nativeResult.IsError) {
				failedResultID = event.ResultID
			}
			emit(proto.TypeUsage, proto.UsagePayload{Usage: usage})
		case "result":
			if !s.matchesInputSession(event.SessionID) || event.SessionID == "" || start.Resume != "" && event.SessionID != start.Resume || usageSession != "" && event.SessionID != usageSession || !s.functionsComplete(false) || !s.steeringComplete() || !mcp.complete() || !commands.complete() {
				failure = fmt.Errorf("claudesdk: invalid native completion or unconfirmed input/result")
				s.settlementErr = failure
				s.invalidate()
			} else {
				result = &event
			}
			terminal = true
		case "error":
			cancelled = event.Code == "cancelled"
			failure = bridgeFailure(event.Code)
			if event.Code == "execution_failed" && event.SessionID != "" && s.matchesInputSession(event.SessionID) &&
				event.SessionID == usageSession && event.ResultID != "" && event.ResultID == failedResultID {
				var code string
				_ = json.Unmarshal(event.EngineErrorCode, &code)
				engineCode, _ = proto.NormalizeEngineFailure(code, nil)
				classifiedFailure = failure
			}
			terminal = true
		default:
			failure = fmt.Errorf("claudesdk: unknown SDK bridge event")
			s.invalidate()
		}
		if failure != nil && !terminal {
			break
		}
	}
	if !settlementReceived && failure == nil {
		failure = fmt.Errorf("claudesdk: Turn settlement is missing")
	}
	if result == nil && failure == nil {
		failure = fmt.Errorf("claudesdk: SDK result is missing")
	}
	// Close result admission before deciding which unanswered calls cancellation settled.
	s.stopFunctions()
	if !s.functionsComplete(cancelled && settlementConfirmed) || !s.steeringComplete() || !mcp.complete() || !commands.complete() {
		settlementConfirmed, reusable, reason = false, false, "unsettled_native_operations"
	}
	mcp.close(start, emit)
	commands.close(start, emit)
	s.stopSteering()
	metadata := map[string]any{proto.DoneMetaAgentSessionType: "claude_session"}
	if id := s.inputSessionID(); id != "" {
		metadata[proto.DoneMetaAgentSessionID] = id
	}
	if failure == nil {
		content.Reset()
		content.WriteString(result.Text)
		metadata[proto.DoneMetaAgentSessionID] = result.SessionID
	}
	s.outcome = proto.DonePayload{Content: content.String(), Usage: usage, Metadata: metadata}
	// Publish the observed cancellation outcome before terminal delivery.
	if s.settlementErr != nil || !settlementReceived || !settlementConfirmed || !reusable || outputLost || s.process.Context().Err() != nil {
		s.owner.retire()
		reusable = false
		if reason == "" {
			reason = "bridge_interrupted"
		}
	}
	if !settlementReceived || !settlementConfirmed {
		s.settlementErr = fmt.Errorf("claudesdk: native Turn settlement is unconfirmed")
	}
	close(s.settled)
	if failure != nil {
		// A valid Turn boundary replaces the former process-exit boundary.
		// A native diagnostic does not itself confirm cancellation or reuse.
		if failure != classifiedFailure || !settlementReceived {
			engineCode = ""
		}
		emit(proto.TypeError, proto.ErrorPayload{Error: failure.Error(), Code: engineCode})
	}
	emit(proto.TypeDone, s.outcome)
	if outputLost {
		s.owner.retire()
		reusable, reason = false, "output_delivery_interrupted"
	}
	if terminalLost {
		s.outputErr = fmt.Errorf("claudesdk: terminal output delivery failed")
	}
	s.turnSettlement = agent.TurnSettlement{Reusable: reusable, Reason: reason}
}
