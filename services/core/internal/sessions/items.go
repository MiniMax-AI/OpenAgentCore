package sessions

import (
	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// ItemPage is one page of a Session's root Items.
type ItemPage struct {
	Items   []v1.Item
	HasMore bool
}
