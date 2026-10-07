//go:build unix

package claudesdk

import (
	"bufio"
	"context"
	"encoding/json"
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// startSingleTurn prepares an Executor as the registry does, starts one Turn
// and closes the Executor once that Turn settles, so each test observes the
// complete native lifecycle.
func startSingleTurn(ctx context.Context, config Config, req proto.PromptRequestPayload, run string, input proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	configuration, err := Declaration.Configuration.Prepare(req)
	if err != nil {
		return nil, err
	}
	resource, err := NewExecutorFactory(config)(ctx, agent.PrepareRequest{PromptRequestPayload: req, Prepared: configuration})
	if err != nil {
		return nil, err
	}
	turn, err := resource.StartTurn(ctx, run, input, out)
	if turn == nil {
		_ = resource.Close(context.Background())
		return nil, err
	}
	go func() { _, _ = turn.AwaitSettlement(context.Background()); _ = resource.Close(context.Background()) }()
	return turn, err
}

func helperTurn(scanner *bufio.Scanner) (func(bridgeEvent), func()) {
	_ = json.NewEncoder(os.Stdout).Encode(bridgeEvent{Type: "executor_ready", Protocol: 3})
	if !scanner.Scan() {
		os.Exit(4)
	}
	var start struct {
		Type   string             `json:"type"`
		TurnID string             `json:"turn_id"`
		Input  proto.MessageInput `json:"input"`
	}
	if json.Unmarshal(scanner.Bytes(), &start) != nil || start.Type != "turn_start" || start.TurnID == "" {
		os.Exit(5)
	}
	return helperTurnOutput(scanner, start.TurnID)
}

func helperTurnOutput(scanner *bufio.Scanner, id string) (func(bridgeEvent), func()) {
	terminal := false
	reusable := true
	encode := func(event bridgeEvent) {
		event.TurnID = id
		_ = json.NewEncoder(os.Stdout).Encode(event)
		if event.Type == "result" || event.Type == "error" {
			terminal = true
			if event.Type == "error" && event.Code != "cancelled" {
				reusable = false
			}
		}
	}
	encode(bridgeEvent{Type: "turn_started"})
	return encode, func() {
		if !terminal {
			return
		}
		reason := ""
		if !reusable {
			reason = "native_error"
		}
		confirmed := true
		encode(bridgeEvent{Type: "turn_settled", Confirmed: &confirmed, Reusable: &reusable, Reason: reason})
		for scanner.Scan() {
		}
	}
}
