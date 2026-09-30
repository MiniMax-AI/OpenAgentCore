package store

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ItemDiagnosticTiming records Core database receipt and settlement, never native
// execution duration. Historical terminal Items can have unknown settlement.
type ItemDiagnosticTiming struct {
	ItemID      string
	StartedAt   time.Time
	CompletedAt *time.Time
}

type TurnDiagnosticsSnapshot struct {
	Session        Session
	Turn           sessions.Turn
	Items          []ItemDiagnosticTiming
	ItemsTruncated bool
}

// GetSessionDiagnosticsSnapshot keeps all existing Session projections in one
// read-only snapshot, including the failure precedence used by the public API.
func (s *Store) GetSessionDiagnosticsSnapshot(ctx context.Context, tenantID, sessionID string) (Session, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return Session{}, err
	}
	var session Session
	err = s.pooled.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		row, err := q.GetSession(ctx, sqlc.GetSessionParams{TenantID: tenant, ID: pgunit.PathID(sessionID)})
		if err != nil {
			return err
		}
		session, err = sessionFromRow(row)
		if err != nil {
			return err
		}
		session, err = readSessionActivity(ctx, q, session)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return session, err
}

// GetTurnDiagnosticsSnapshot reads only root Turns and their root Items. Its
// bounded query and public projection share the same committed snapshot.
func (s *Store) GetTurnDiagnosticsSnapshot(ctx context.Context, tenantID, sessionID, turnID string) (TurnDiagnosticsSnapshot, error) {
	params, err := publicTurnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return TurnDiagnosticsSnapshot{}, err
	}
	result := TurnDiagnosticsSnapshot{Items: []ItemDiagnosticTiming{}}
	err = s.pooled.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		turn, err := q.GetTurn(ctx, params)
		if err != nil {
			return err
		}
		row, err := q.GetSession(ctx, sqlc.GetSessionParams{TenantID: params.TenantID, ID: params.SessionID})
		if err != nil {
			return err
		}
		result.Session, err = sessionFromRow(row)
		if err != nil {
			return err
		}
		result.Turn = turnFromRow(turn)
		rows, err := q.ListTurnItemDiagnostics(ctx, sqlc.ListTurnItemDiagnosticsParams{SessionID: params.SessionID, TurnID: params.ID})
		if err != nil {
			return err
		}
		result.ItemsTruncated = len(rows) > 1000
		if result.ItemsTruncated {
			rows = rows[:1000]
		}
		for _, row := range rows {
			item := ItemDiagnosticTiming{ItemID: uuid.UUID(row.ID.Bytes).String(), StartedAt: row.CreatedAt.Time}
			if row.SettledAt.Valid {
				at := row.SettledAt.Time
				item.CompletedAt = &at
			}
			result.Items = append(result.Items, item)
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return TurnDiagnosticsSnapshot{}, ErrNotFound
	}
	return result, err
}
