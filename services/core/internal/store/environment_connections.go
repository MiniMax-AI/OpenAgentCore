package store

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ReplaceEnvironmentConnection starts an ordered generation, without claiming a connection.
// The producer serializes replacements and never retries an older replacement after its successor.
func (s *Store) ReplaceEnvironmentConnection(ctx context.Context, tenant, environment, generation string) error {
	gen, err := parseConnectionGeneration(generation)
	if err != nil {
		return err
	}
	return s.withEnvironmentConnection(ctx, tenant, environment, func(ctx context.Context, q *sqlc.Queries, row sqlc.GetSessionEnvironmentRow) error {
		if row.Environment.Status == "failed" || row.Environment.Status == "expired" {
			return ErrInvalidInput
		}
		old, err := q.GetEnvironmentConnection(ctx, row.Environment.ID)
		if err == nil && old.Generation == gen {
			return nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := q.ReplaceEnvironmentConnection(ctx, sqlc.ReplaceEnvironmentConnectionParams{EnvironmentID: row.Environment.ID, Generation: gen}); err != nil {
			return err
		}
		if row.Environment.Status == "connected" {
			return recordEnvironmentConnection(ctx, q, row, "disconnected")
		}
		return nil
	})
}

// ObserveEnvironmentConnection commits only a newer observation from the current generation.
// Connectivity is transport evidence, not native preparation readiness or process quiescence.
func (s *Store) ObserveEnvironmentConnection(ctx context.Context, tenant, environment, generation string, revision int64, connected bool) error {
	gen, err := parseConnectionGeneration(generation)
	if err != nil || revision <= 0 {
		return ErrInvalidInput
	}
	return s.withEnvironmentConnection(ctx, tenant, environment, func(ctx context.Context, q *sqlc.Queries, row sqlc.GetSessionEnvironmentRow) error {
		current, err := q.GetEnvironmentConnection(ctx, row.Environment.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if current.Generation != gen || current.Revision >= revision {
			return nil
		}
		if row.Environment.Status == "failed" || row.Environment.Status == "expired" {
			return ErrInvalidInput
		}
		if err := q.AdvanceEnvironmentConnection(ctx, sqlc.AdvanceEnvironmentConnectionParams{EnvironmentID: row.Environment.ID, Revision: revision}); err != nil {
			return err
		}
		status := "disconnected"
		if connected {
			status = "connected"
		}
		if row.Environment.Status == status {
			return nil
		}
		return recordEnvironmentConnection(ctx, q, row, status)
	})
}

func (s *Store) withEnvironmentConnection(ctx context.Context, tenant, environment string, apply func(context.Context, *sqlc.Queries, sqlc.GetSessionEnvironmentRow) error) error {
	if s.executionLease == nil {
		return errors.New("Environment observations require an execution lease")
	}
	ctx, cancel := context.WithTimeout(ctx, executionTransactionTimeout)
	defer cancel()
	owned, err := s.GetEnvironment(ctx, tenant, environment)
	if err != nil {
		return err
	}
	tenantID, err := parseID(tenant)
	if err != nil {
		return err
	}
	return s.withPublicSession(ctx, tenant, owned.SessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		row, err := q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: tenantID, ID: session})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return withEnvironmentInputActivity(ctx, q, session, func() error { return apply(ctx, q, row) })
	})
}

func recordEnvironmentConnection(ctx context.Context, q *sqlc.Queries, row sqlc.GetSessionEnvironmentRow, status string) error {
	if _, err := storedEnvironmentType(row); err != nil {
		return err
	}
	if status != "connected" && status != "disconnected" {
		return ErrInvalidInput
	}
	if err := q.SetEnvironmentConnectionStatus(ctx, sqlc.SetEnvironmentConnectionStatusParams{ID: row.Environment.ID, Status: status}); err != nil {
		return err
	}
	return recordEnvironmentState(ctx, q, row, status)
}

// recordEnvironmentState appends the pinned Environment state event for an
// already committed status. A failure uses the observed official error; the
// failed step travels only in the separate error event and Session error.
func recordEnvironmentState(ctx context.Context, q *sqlc.Queries, row sqlc.GetSessionEnvironmentRow, status string) error {
	kind, err := storedEnvironmentType(row)
	if err != nil {
		return err
	}
	state := &v1.SessionEnvironmentState{ID: uuid.UUID(row.Environment.ID.Bytes).String(), Type: kind, Status: status}
	if status == "failed" {
		state.Error = &v1.StreamError{Type: "environment_error", Code: "environment_connection_failed", Message: "The environment failed to connect."}
	}
	return recordSessionChange(ctx, q, row.Environment.SessionID, SessionChange{Event: v1.SessionEvent{
		Type:        "agent.session.environment." + status,
		Environment: state,
	}})
}

func storedEnvironmentType(row sqlc.GetSessionEnvironmentRow) (string, error) {
	var config struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(row.Configuration, &config); err != nil || (config.Type != "self_hosted" && config.Type != "openai_hosted") {
		return "", errors.New("invalid stored Environment type")
	}
	return config.Type, nil
}

func parseConnectionGeneration(value string) (pgtype.UUID, error) {
	id, err := parseID(value)
	if err != nil || id.Bytes == [16]byte{} {
		return pgtype.UUID{}, ErrInvalidInput
	}
	return id, nil
}
