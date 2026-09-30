package sessionpg

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// WithTurnJournal runs apply in a Session transaction on the lease, including
// for a publicly deleted Session.
func (e *Execution) WithTurnJournal(ctx context.Context, tenantID, sessionID string, apply func(context.Context, sessions.TurnJournalTx) error) error {
	tenant, err := parseID(tenantID)
	if err != nil {
		return err
	}
	session, err := parseID(sessionID)
	if err != nil {
		return err
	}
	return WithSession(ctx, e.lease, tenant, session, func(ctx context.Context, q *sqlc.Queries, _ sessions.LockedSession) error {
		return apply(ctx, BindSession(q, tenant, session))
	})
}
