package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

// SavedAgent is reusable configuration owned by an execution tenant. It has no
// engine binding or live execution state; Session snapshots are separate objects.
type SavedAgent struct {
	ID            string
	TenantID      string
	Metadata      map[string]string
	Configuration json.RawMessage
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type CreateAgentInput struct {
	ModelProvider *v1.ModelProviderInput
	Metadata      map[string]string
	Configuration json.RawMessage
}

// CreateAgent stores caller-validated, credential-free configuration. Public
// defaults and schema validation belong to the API, not an execution adapter.
// Each call creates a new resource; this primitive supplies no retry semantics.
func (s *Store) CreateAgent(ctx context.Context, tenantID string, input CreateAgentInput) (SavedAgent, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return SavedAgent{}, err
	}
	metadata, err := encodeMetadata(input.Metadata)
	if err != nil {
		return SavedAgent{}, err
	}
	if len(input.Configuration) == 0 || len(input.Configuration) > 512*1024 {
		return SavedAgent{}, fmt.Errorf("%w: configuration must be an object of at most 512 KiB", ErrInvalidInput)
	}
	configuration, err := canonicalJSONObject(input.Configuration)
	if err != nil {
		return SavedAgent{}, err
	}
	if err := validateAgentModelExecution(configuration, input.ModelProvider); err != nil {
		return SavedAgent{}, err
	}
	var created SavedAgent
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		row, err := q.CreateAgent(ctx, sqlc.CreateAgentParams{
			ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: tenant,
			Metadata: metadata, Configuration: configuration,
		})
		if err != nil {
			return err
		}
		if err := s.saveAgentModelExecution(ctx, q, uuid.UUID(tenant.Bytes).String(), row.ID, input.ModelProvider); err != nil {
			return err
		}
		created, err = agentFromRow(row)
		if err != nil {
			return err
		}
		return recordWriteAudit(ctx, q, tenantID, "create", "agent", created.ID, "", AuditResource{Type: "agent", ID: created.ID})
	})
	if err != nil {
		return SavedAgent{}, fmt.Errorf("create agent: %w", err)
	}
	return created, nil
}

// GetAgent scopes every lookup to the authenticated caller's tenant.
func (s *Store) GetAgent(ctx context.Context, tenantID, agentID string) (SavedAgent, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return SavedAgent{}, err
	}
	id, err := parseID(agentID)
	if err != nil {
		return SavedAgent{}, err
	}
	row, err := s.queries.GetAgent(ctx, sqlc.GetAgentParams{TenantID: tenant, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return SavedAgent{}, ErrNotFound
	}
	if err != nil {
		return SavedAgent{}, fmt.Errorf("get agent: %w", err)
	}
	return agentFromRow(row)
}

func agentFromRow(row sqlc.Agent) (SavedAgent, error) {
	agent := SavedAgent{
		ID: uuid.UUID(row.ID.Bytes).String(), TenantID: uuid.UUID(row.TenantID.Bytes).String(),
		Configuration: row.Configuration, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
	if err := json.Unmarshal(row.Metadata, &agent.Metadata); err != nil {
		return SavedAgent{}, fmt.Errorf("decode agent metadata: %w", err)
	}
	return agent, nil
}
