package sessions

import (
	"context"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

// SubagentReader reads the Subagents a visible Session observed, with their
// Turns and Items. A Subagent, Turn or Item outside the named Session or
// Subagent is ErrNotFound. A list limit outside 1..100 is ErrInvalidInput. A
// Subagent or Subagent Turn cursor outside its list, including a malformed
// one, is ErrResourceCursor; an Item cursor is ErrItemCursor.
type SubagentReader interface {
	// GetSubagent returns one Subagent of the Session, including a nested or
	// closed one.
	GetSubagent(ctx context.Context, tenantID, sessionID, subagentID string) (v1.Subagent, error)
	// ListSubagents returns one page of the Session's Subagents in opening
	// order.
	ListSubagents(ctx context.Context, tenantID, sessionID, cursor string, limit int, ascending bool) (v1.SubagentList, error)
	// ListSubagentItems returns one page of the Subagent's Items.
	ListSubagentItems(ctx context.Context, tenantID, sessionID, subagentID, cursor string, limit int, ascending bool) (v1.ItemList, error)
	// GetSubagentTurn returns one of the Subagent's Turns. Its agent_id is the
	// Session's Agent ID and its subagent_id names the child.
	GetSubagentTurn(ctx context.Context, tenantID, sessionID, subagentID, turnID string) (v1.Turn, error)
	// ListSubagentTurns returns one page of the Subagent's Turns in creation
	// order.
	ListSubagentTurns(ctx context.Context, tenantID, sessionID, subagentID, cursor string, limit int, ascending bool) (v1.TurnList, error)
	// ListSubagentTurnItems returns one page of the Items of one of the
	// Subagent's Turns. An empty Turn ID is ErrInvalidInput.
	ListSubagentTurnItems(ctx context.Context, tenantID, sessionID, subagentID, turnID, cursor string, limit int, ascending bool) (v1.ItemList, error)
}
