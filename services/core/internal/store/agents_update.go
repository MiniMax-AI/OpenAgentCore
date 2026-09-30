package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
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
	id, err := parseID(agentID)
	if err != nil {
		return SavedAgent{}, err
	}
	raw := input.Configuration
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > 512*1024 {
		return SavedAgent{}, ErrInvalidInput
	}
	raw, err = canonicalJSONObject(raw)
	if err != nil {
		return SavedAgent{}, err
	}
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(raw, &patch); err != nil {
		return SavedAgent{}, err
	}
	var metadata []byte
	if input.Metadata != nil {
		metadata, err = encodeMetadata(*input.Metadata)
		if err != nil {
			return SavedAgent{}, err
		}
	}
	var updated SavedAgent
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
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
		merged, err = canonicalJSONObject(merged)
		if err != nil {
			return err
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
			metadata = row.Metadata
		}
		row, err = q.UpdateAgent(ctx, sqlc.UpdateAgentParams{TenantID: tenant, ID: id, Configuration: merged, Metadata: metadata})
		if err != nil {
			return err
		}
		updated, err = agentFromRow(row)
		if err != nil {
			return err
		}
		return recordWriteAudit(ctx, q, tenantID, "update", "agent", updated.ID, "")
	})
	if err != nil {
		return SavedAgent{}, fmt.Errorf("update agent: %w", err)
	}
	return updated, nil
}
