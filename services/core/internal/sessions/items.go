package sessions

import (
	"context"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// ItemPage is one page of a Session's root Items.
type ItemPage struct {
	Items   []v1.Item
	HasMore bool
}

// ItemReader reads a Session's root Items.
type ItemReader interface {
	// ListItems returns one page of the visible Session's root Items in
	// first-observation order, ascending or descending. A limit outside 1..100
	// is ErrInvalidInput. A cursor that is not an Item of the Session,
	// including a malformed one, is ErrItemCursor.
	ListItems(ctx context.Context, tenantID, sessionID, cursor string, limit int, ascending bool) (ItemPage, error)
}
