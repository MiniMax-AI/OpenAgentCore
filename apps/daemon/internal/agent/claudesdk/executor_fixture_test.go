//go:build unix

package claudesdk

import (
	"bufio"
	"encoding/json"
	"os"
)

func helperTurn(scanner *bufio.Scanner, request *startRequest) (func(bridgeEvent), func()) {
	_ = json.NewEncoder(os.Stdout).Encode(bridgeEvent{Type: "executor_ready", Protocol: 3})
	if !scanner.Scan() {
		os.Exit(4)
	}
	var start struct {
		Type   string          `json:"type"`
		TurnID string          `json:"turn_id"`
		Input  json.RawMessage `json:"input"`
	}
	if json.Unmarshal(scanner.Bytes(), &start) != nil || start.Type != "turn_start" || start.TurnID == "" || json.Unmarshal(start.Input, &request.Input) != nil {
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
