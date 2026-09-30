package store

import (
	"context"
	"errors"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// EnvironmentInputActivity is the reservation-owned override before a newer Turn exists.
type EnvironmentInputActivity struct {
	Status        string    `json:"status"`
	EnvironmentID string    `json:"environment_id,omitempty"`
	Failure       string    `json:"failure,omitempty"`
	LastActiveAt  time.Time `json:"last_active_at"`
}

func environmentInputActivity(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) (*EnvironmentInputActivity, error) {
	activity, _, err := environmentInputState(ctx, q, session)
	return activity, err
}

// environmentInputState also reports whether the latest reservation is still
// pending and can start a Turn, including a provisioning hosted initial input
// that has no public activity. While a Turn is active or newer than it, the
// reservation is not reported.
func environmentInputState(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) (*EnvironmentInputActivity, bool, error) {
	row, err := q.GetEnvironmentInputActivity(ctx, session)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	pending := row.State == EnvironmentInputPending
	activity := &EnvironmentInputActivity{Status: "idle", LastActiveAt: row.CreatedAt.Time}
	if row.SettledAt.Valid {
		activity.LastActiveAt = row.SettledAt.Time
	}
	if row.State == EnvironmentInputFailed {
		activity.Status, activity.Failure = "failed", "environment_unavailable"
		if row.FailureCode.Valid {
			activity.Failure = row.FailureCode.String
		}
	}
	if row.IsInitial && row.State == EnvironmentInputExpired {
		activity.Status = "failed"
	}
	if row.EnvironmentType == "openai_hosted" && row.IsInitial &&
		(row.State == EnvironmentInputPending || row.State == EnvironmentInputCancelled) {
		// No Turn has started. The pinned Session contract permits idle while a
		// hosted Environment provisions; neither a caller action nor an invented
		// in-progress/idle transition is appropriate here.
		return nil, pending, nil
	}
	if pending && row.EnvironmentType != "openai_hosted" && row.ConnectionStatus != "connected" {
		activity.Status = "requires_action"
		activity.EnvironmentID = uuid.UUID(row.EnvironmentID.Bytes).String()
	}
	return activity, pending, nil
}

func withEnvironmentInputActivity(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, apply func() error) error {
	before, err := environmentInputActivity(ctx, q, session)
	if err != nil {
		return err
	}
	if err := apply(); err != nil {
		return err
	}
	after, pending, err := environmentInputState(ctx, q, session)
	if err != nil || after == nil {
		// Admitted input is represented by the normal Turn and Session events.
		return err
	}
	if before != nil && before.Status == after.Status && before.EnvironmentID == after.EnvironmentID && before.Failure == after.Failure {
		return nil
	}
	usage, err := q.SessionTokenUsage(ctx, session)
	if err != nil {
		return err
	}
	return recordSessionChange(ctx, q, session, SessionChange{
		Event:                    v1.SessionEvent{Type: "agent.session." + after.Status},
		EnvironmentInputActivity: after, SessionUsage: usage, Settled: !pending,
	})
}

func (s *Store) withEnvironmentInputSession(ctx context.Context, tenant, session string, apply func(context.Context, *sqlc.Queries, pgtype.UUID) error) error {
	return s.withPublicSession(ctx, tenant, session, func(ctx context.Context, q *sqlc.Queries, id pgtype.UUID) error {
		return withEnvironmentInputActivity(ctx, q, id, func() error { return apply(ctx, q, id) })
	})
}
