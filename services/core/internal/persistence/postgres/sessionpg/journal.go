package sessionpg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// LoadJournalTurn reads the tenant's Turn in the Session.
func (t *SessionTx) LoadJournalTurn(ctx context.Context, turn string) (sessions.JournalTurn, bool, error) {
	id, err := parseID(turn)
	if err != nil {
		return sessions.JournalTurn{}, false, err
	}
	row, err := t.q.GetTurn(ctx, sqlc.GetTurnParams{TenantID: t.tenant, SessionID: t.session, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.JournalTurn{}, false, nil
	}
	if err != nil {
		return sessions.JournalTurn{}, false, err
	}
	return sessions.JournalTurn{Status: row.Status, EventCount: row.EventCount, EventBytes: row.EventBytes}, true, nil
}

// MatchEvents compares the Turn's journal entries from first with events.
func (t *SessionTx) MatchEvents(ctx context.Context, turn string, first int32, events []sessions.ExecutionEvent) (bool, error) {
	id, err := parseID(turn)
	if err != nil {
		return false, err
	}
	batch, err := json.Marshal(events)
	if err != nil {
		return false, err
	}
	return t.q.MatchTurnEventBatch(ctx, sqlc.MatchTurnEventBatchParams{SessionID: t.session, TurnID: id, FirstOrdinal: first, EventCount: int32(len(events)), Batch: batch})
}

// InsertEvents records events at the Turn's journal positions from first. The
// database stamps each entry.
func (t *SessionTx) InsertEvents(ctx context.Context, turn string, first int32, events []sessions.ExecutionEvent) error {
	id, err := parseID(turn)
	if err != nil {
		return err
	}
	batch, err := json.Marshal(events)
	if err != nil {
		return err
	}
	return t.q.InsertTurnEventBatch(ctx, sqlc.InsertTurnEventBatchParams{SessionID: t.session, TurnID: id, FirstOrdinal: first, Batch: batch})
}

// InsertEvent records event at the Turn's journal position ordinal.
func (t *SessionTx) InsertEvent(ctx context.Context, turn string, ordinal int32, event sessions.ExecutionEvent) error {
	id, err := parseID(turn)
	if err != nil {
		return err
	}
	return t.q.InsertTurnEvent(ctx, sqlc.InsertTurnEventParams{SessionID: t.session, TurnID: id, Ordinal: ordinal, Kind: event.Kind, Payload: event.Payload})
}

// CountEvents adds entries to the Turn's journal counters.
func (t *SessionTx) CountEvents(ctx context.Context, turn string, count int32, size int64) error {
	id, err := parseID(turn)
	if err != nil {
		return err
	}
	return t.q.CountTurnEvent(ctx, sqlc.CountTurnEventParams{SessionID: t.session, ID: id, EventCount: count, PayloadBytes: size})
}

// LoadEventSources reads the Turn's journal entries from first, in order.
func (t *SessionTx) LoadEventSources(ctx context.Context, turn string, first int32) ([]sessions.Source, error) {
	id, err := parseID(turn)
	if err != nil {
		return nil, err
	}
	rows, err := t.q.ItemEventSources(ctx, sqlc.ItemEventSourcesParams{SessionID: t.session, TurnID: id, Ordinal: first})
	if err != nil {
		return nil, err
	}
	sources := make([]sessions.Source, len(rows))
	for i, row := range rows {
		sources[i] = sessions.Source{Turn: uuidString(row.TurnID), Kind: row.Kind, Sequence: int64(row.Ordinal), Payload: row.Payload, CreatedAt: row.CreatedAt.Time}
	}
	return sources, nil
}

// LoadInputSource reads the Session's admitted input at sequence.
func (t *SessionTx) LoadInputSource(ctx context.Context, sequence int64) (sessions.Source, error) {
	row, err := t.q.ItemInputSource(ctx, sqlc.ItemInputSourceParams{SessionID: t.session, Sequence: sequence})
	if err != nil {
		return sessions.Source{}, err
	}
	return sessions.Source{Turn: uuidString(row.TurnID), Kind: row.Kind, Sequence: row.Sequence, Payload: row.Payload, CreatedAt: row.CreatedAt.Time}, nil
}

// uuidString formats an identifier, and an absent one as empty.
func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
