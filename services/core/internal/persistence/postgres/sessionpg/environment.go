package sessionpg

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// LoadEnvironmentInput reads the Session's latest Environment input
// reservation that no Turn has admitted or superseded, with the facts of its
// Environment, and nil when there is none. sessions.InputActivity projects it.
func LoadEnvironmentInput(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) (*sessions.EnvironmentInputState, error) {
	row, err := q.GetEnvironmentInputActivity(ctx, session)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	state := &sessions.EnvironmentInputState{
		State: row.State, Initial: row.IsInitial, CreatedAt: row.CreatedAt.Time, FailureCode: row.FailureCode.String,
		EnvironmentID: uuid.UUID(row.EnvironmentID.Bytes).String(), EnvironmentType: row.EnvironmentType, EnvironmentStatus: row.ConnectionStatus,
	}
	if row.SettledAt.Valid {
		state.SettledAt = row.SettledAt.Time
	}
	return state, nil
}
