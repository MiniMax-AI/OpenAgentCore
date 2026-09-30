package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/jsonobject"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func createSessionEnvironment(ctx context.Context, q *sqlc.Queries, session sqlc.Session) error {
	var snapshot struct {
		Environment *struct {
			Type string `json:"type"`
		} `json:"environment"`
	}
	if err := json.Unmarshal(session.Configuration, &snapshot); err != nil {
		return fmt.Errorf("%w: invalid environment configuration", sessions.ErrInvalidInput)
	}
	if snapshot.Environment == nil || snapshot.Environment.Type == "none" {
		return nil
	}
	switch snapshot.Environment.Type {
	case "self_hosted", "openai_hosted":
		return q.CreateEnvironment(ctx, sqlc.CreateEnvironmentParams{
			ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, SessionID: session.ID,
		})
	default:
		return fmt.Errorf("%w: unsupported environment type", sessions.ErrInvalidInput)
	}
}

func (s *Store) GetEnvironment(ctx context.Context, tenantID, environmentID string) (sessions.Environment, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.Environment{}, err
	}
	id := pgunit.PathID(environmentID)
	row, err := s.queries.GetEnvironment(ctx, sqlc.GetEnvironmentParams{TenantID: tenant, ID: id})
	return environmentFromRow(row.Environment, row.TenantID, row.Configuration, err)
}

func (s *Store) GetSessionEnvironment(ctx context.Context, tenantID, sessionID string) (sessions.Environment, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.Environment{}, err
	}
	id, err := parseID(sessionID)
	if err != nil {
		return sessions.Environment{}, err
	}
	row, err := s.queries.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: tenant, ID: id})
	return environmentFromRow(row.Environment, row.TenantID, row.Configuration, err)
}

func environmentFromRow(row sqlc.Environment, tenant pgtype.UUID, configuration []byte, err error) (sessions.Environment, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Environment{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.Environment{}, fmt.Errorf("get environment: %w", err)
	}
	configuration, err = jsonobject.Normalize(configuration)
	if err != nil {
		return sessions.Environment{}, fmt.Errorf("decode environment configuration: %w: %w", sessions.ErrInvalidInput, err)
	}
	return sessions.Environment{
		ID: uuid.UUID(row.ID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(),
		TenantID: uuid.UUID(tenant.Bytes).String(), Status: row.Status,
		Initialization: row.Initialization, CreatedAt: row.CreatedAt.Time, Configuration: configuration,
	}, nil
}
