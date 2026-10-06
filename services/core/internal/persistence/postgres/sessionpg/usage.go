package sessionpg

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
)

// LoadUsage reads the Session's public usage: the sum of its root Turns once
// every one has ended with recorded usage, and null otherwise.
func LoadUsage(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) (json.RawMessage, error) {
	return q.SessionTokenUsage(ctx, session)
}

// PutTurnUsage replaces a root Turn's recorded usage with a measurement.
func (t *SessionTx) PutTurnUsage(ctx context.Context, turnID string, usage v1.TokenUsage) error {
	turn, err := parseID(turnID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(usage)
	if err != nil {
		return err
	}
	return t.q.PutTurnUsage(ctx, sqlc.PutTurnUsageParams{SessionID: t.session, ID: turn, TokenUsage: payload})
}
