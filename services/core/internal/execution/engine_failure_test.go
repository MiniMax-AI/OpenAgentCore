package execution

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestEngineClassificationSurvivesDrainWithoutChangingSettlement(t *testing.T) {
	for _, prior := range []string{"", "event_persistence_failed", "cancel_unconfirmed", "event_stream_incomplete"} {
		result := Result{ErrorCode: prior}
		writer := &recoveringWriter{}
		j := journal{writer: writer, next: 1}
		events := make(chan proto.Envelope, 3)
		failure, _ := proto.NewEnvelope(proto.TypeError, "run", proto.ErrorPayload{Error: "raw native text", Code: "authentication_error"})
		usage, _ := proto.NewEnvelope(proto.TypeUsage, "run", proto.Usage{InputTokens: 7})
		done, _ := proto.NewEnvelope(proto.TypeDone, "run", map[string]any{"metadata": map[string]any{proto.DoneMetaAgentSessionID: "native"}})
		events <- failure
		events <- usage
		events <- done
		close(events)
		if err := j.drain(events, &result); err != nil {
			t.Fatal(err)
		}
		if result.ErrorCode != prior || result.EngineErrorCode != "authentication_error" || result.Done.Usage.InputTokens != 7 || result.Done.Metadata[proto.DoneMetaAgentSessionID] != "native" {
			t.Fatal(result)
		}
		reply := cancellationResult{ack: proto.InteractionDecisionAckPayload{Applied: true, Outcome: &proto.DonePayload{Usage: proto.Usage{InputTokens: 9}, Metadata: map[string]any{proto.DoneMetaAgentSessionID: "native"}}}}
		if err := recordCancellation(context.Background(), &j, reply, &result); err != nil {
			t.Fatal(err)
		}
		if result.ErrorCode != prior || result.Done.Usage.InputTokens != 9 {
			t.Fatal(result)
		}
		raw, _ := json.Marshal(result)
		var persisted Result
		if json.Unmarshal(raw, &persisted) != nil || persisted.EngineErrorCode != result.EngineErrorCode {
			t.Fatal(string(raw))
		}
		unknown := proto.Envelope{Type: proto.TypeError, Payload: json.RawMessage(`{"error":"new","code":{"arbitrary":"value"},"http_status":"secret"}`)}
		if err := result.mergeObservation(unknown); err != nil || result.EngineErrorCode != "" || result.ErrorCode != prior || result.Done.Usage.InputTokens != 9 {
			t.Fatal(err, result)
		}
	}
}

func TestClassifiedErrorCannotOverrideCancellationReceipt(t *testing.T) {
	for _, drainFirst := range []bool{true, false} {
		result := Result{ErrorCode: "engine_failed"}
		j := journal{writer: &recoveringWriter{}, next: 1}
		failure, _ := proto.NewEnvelope(proto.TypeError, "run", proto.ErrorPayload{Code: "rate_limit_exceeded"})
		events := make(chan proto.Envelope, 1)
		events <- failure
		close(events)
		ack := make(chan cancellationResult, 1)
		ack <- cancellationResult{ack: proto.InteractionDecisionAckPayload{Applied: true, Outcome: &proto.DonePayload{Usage: proto.Usage{InputTokens: 9}, Metadata: map[string]any{proto.DoneMetaAgentSessionID: "native"}}}}
		if drainFirst {
			if err := j.drain(events, &result); err != nil {
				t.Fatal(err)
			}
		}
		status := finishDelivery(context.Background(), &j, &result, ack, false, nil)
		if !drainFirst {
			if err := j.drain(events, &result); err != nil {
				t.Fatal(err)
			}
		}
		if status != sessions.TurnCancelled || result.Done.Usage.InputTokens != 9 || result.Done.Metadata[proto.DoneMetaAgentSessionID] != "native" || result.EngineErrorCode != "rate_limit_exceeded" {
			t.Fatalf("receipt lost authority: %s %+v", status, result)
		}
	}
}
