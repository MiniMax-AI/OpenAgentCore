package items

import (
	"encoding/json"
	"strconv"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

func inputMessages(turn string, sequence int64, raw json.RawMessage) []Update {
	var p struct {
		Input []struct {
			Role    string           `json:"role"`
			Content []v1.ItemContent `json:"content"`
		} `json:"input"`
	}
	// Session admission stores any JSON object; only public messages project.
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil
	}
	key := "input:" + strconv.FormatInt(sequence, 10)
	var updates []Update
	for i, input := range p.Input {
		if input.Role != "user" || len(input.Content) == 0 {
			continue
		}
		valid := true
		for _, c := range input.Content {
			if !((c.Type == "input_text" && c.Text != nil) || (c.Type == "input_image" && c.ImageURL != "")) {
				valid = false
			}
		}
		if !valid {
			continue
		}
		item := v1.Item{ID: Identity(turn, key+":"+strconv.Itoa(i)), TurnID: turn, Type: "message", Status: "completed", Role: "user", Content: input.Content}
		updates = append(updates, Update{Item: item})
	}
	return updates
}
