// Package v1 contains the supported wire types from the pinned Agents API.
package v1

import "encoding/json"

type agentWire Agent

// agentResponse renders a Session Agent with explicit reasoning keys.
type agentResponse struct {
	agentWire
	Reasoning reasoningResponse `json:"reasoning"`
}

// MarshalJSON renders the Session Agent's reasoning with explicit null keys.
// The stored Session configuration keeps its original encoding.
func (s Session) MarshalJSON() ([]byte, error) {
	type wire Session
	return json.Marshal(struct {
		wire
		Agent agentResponse `json:"agent"`
	}{wire(s), agentResponse{agentWire(s.Agent), reasoningResponse(s.Agent.Reasoning)}})
}
