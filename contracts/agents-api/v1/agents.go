package v1

import "encoding/json"

// MarshalJSON renders reasoning with explicit null keys. The stored
// SavedAgentConfiguration keeps its original encoding.
func (a SavedAgent) MarshalJSON() ([]byte, error) {
	type wire SavedAgent
	return json.Marshal(struct {
		wire
		Reasoning reasoningResponse `json:"reasoning"`
	}{wire(a), reasoningResponse(a.Reasoning)})
}
