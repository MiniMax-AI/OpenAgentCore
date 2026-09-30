package sessionpg

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// AppendChanges journals public changes in order. Each gets a new event ID and
// the Session's next sequence position, and commits with the caller's state.
// Text PostgreSQL cannot store is textvalue.ErrUnstorable.
func AppendChanges(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, changes ...sessions.SessionChange) error {
	for _, change := range changes {
		change.Event.EventID = uuid.NewString()
		change.Event.SessionID = uuid.UUID(session.Bytes).String()
		payload, err := json.Marshal(change)
		if err != nil {
			return err
		}
		if err := q.AppendSessionEvent(ctx, sqlc.AppendSessionEventParams{ID: session, Payload: payload}); err != nil {
			return storable(err)
		}
	}
	return nil
}

// PruneChanges drops the Session's journaled changes beyond the sessions
// retention bounds. Callers run it after the transaction's last change.
func PruneChanges(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
	return q.PruneSessionEvents(ctx, sqlc.PruneSessionEventsParams{SessionID: session, RetainedEvents: sessions.RetainedChanges, RetainedBytes: sessions.RetainedChangeBytes})
}
