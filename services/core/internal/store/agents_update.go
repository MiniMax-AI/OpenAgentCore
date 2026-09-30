package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/jsonobject"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/metadata"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// UpdateAgentInput contains validated field replacements, not a full snapshot.
// Core extension subfields merge independently. A nil metadata pointer preserves
// the existing map; a supplied map replaces it. ModelProviderSet distinguishes
// omission from replacement or an explicit nil provider, which clears the secret.
type UpdateAgentInput struct {
	ModelProvider    *v1.ModelProviderInput
	ModelProviderSet bool
	Configuration    json.RawMessage
	Metadata         *map[string]string
}

func (s *Store) UpdateAgent(ctx context.Context, tenantID, agentID string, input UpdateAgentInput) (SavedAgent, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return SavedAgent{}, err
	}
	id := pgunit.PathID(agentID)
	raw := input.Configuration
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > 512*1024 {
		return SavedAgent{}, ErrInvalidInput
	}
	raw, err = jsonobject.Normalize(raw)
	if err != nil {
		return SavedAgent{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(raw, &patch); err != nil {
		return SavedAgent{}, err
	}
	var encodedMetadata []byte
	if input.Metadata != nil {
		encodedMetadata, err = metadata.Encode(*input.Metadata)
		if err != nil {
			return SavedAgent{}, fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
	}
	var updated SavedAgent
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		row, err := q.LockAgent(ctx, sqlc.LockAgentParams{TenantID: tenant, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var configuration map[string]json.RawMessage
		if err := json.Unmarshal(row.Configuration, &configuration); err != nil {
			return err
		}
		if err := mergeAgentConfiguration(configuration, patch); err != nil {
			return err
		}
		merged, err := json.Marshal(configuration)
		if err != nil {
			return err
		}
		merged, err = jsonobject.Normalize(merged)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
		if len(merged) > 512*1024 {
			return ErrInvalidInput
		}
		if err := validateAgentModelExecution(merged, input.ModelProvider); err != nil {
			return err
		}
		if input.ModelProviderSet {
			if err := s.saveAgentModelExecution(ctx, q, uuid.UUID(tenant.Bytes).String(), id, input.ModelProvider); err != nil {
				return err
			}
		}
		if input.Metadata == nil {
			encodedMetadata = row.Metadata
		}
		row, err = q.UpdateAgent(ctx, sqlc.UpdateAgentParams{TenantID: tenant, ID: id, Configuration: merged, Metadata: encodedMetadata})
		if err != nil {
			return err
		}
		updated, err = agentFromRow(row)
		if err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "update", "agent", updated.ID, "")
	})
	if err != nil {
		return SavedAgent{}, fmt.Errorf("update agent: %w", err)
	}
	return updated, nil
}
