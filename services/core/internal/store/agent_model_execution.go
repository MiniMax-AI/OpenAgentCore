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
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) saveAgentModelExecution(ctx context.Context, q *sqlc.Queries, tenant string, agent pgtype.UUID, provider *v1.ModelProviderInput) error {
	if provider == nil {
		return q.DeleteAgentModelExecution(ctx, agent)
	}
	raw, err := json.Marshal(provider)
	if err != nil {
		return err
	}
	encrypted, err := s.credentialCipher.SealAgentModelExecution(raw, tenant, uuid.UUID(agent.Bytes).String())
	if err != nil {
		return ErrCredentialStorageUnavailable
	}
	return q.SaveAgentModelExecution(ctx, sqlc.SaveAgentModelExecutionParams{AgentID: agent, EncryptedConfig: encrypted})
}

// GetAgentForSession reads the safe configuration and secret from one database
// snapshot. Explicit Session provider overrides never require Agent decryption.
func (s *Store) GetAgentForSession(ctx context.Context, tenantID, agentID string, inheritProvider bool) (SavedAgent, *v1.ModelProviderInput, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return SavedAgent{}, nil, err
	}
	id, err := parseID(agentID)
	if err != nil {
		return SavedAgent{}, nil, err
	}
	row, err := s.queries.GetAgentForSession(ctx, sqlc.GetAgentForSessionParams{TenantID: tenant, AgentID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return SavedAgent{}, nil, ErrNotFound
	}
	if err != nil {
		return SavedAgent{}, nil, fmt.Errorf("get agent for session: %w", err)
	}
	agent, err := agentFromRow(sqlc.Agent{ID: row.ID, TenantID: row.TenantID, Metadata: row.Metadata, Configuration: row.Configuration, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	if err != nil || !inheritProvider {
		return agent, nil, err
	}
	var config struct {
		Core *v1.SavedAgentCore `json:"x_agents_core"`
	}
	if err := json.Unmarshal(agent.Configuration, &config); err != nil {
		return SavedAgent{}, nil, ErrInvalidInput
	}
	if config.Core == nil || config.Core.ModelProvider == nil {
		return agent, nil, nil
	}
	raw, err := s.credentialCipher.OpenAgentModelExecution(row.EncryptedConfig, uuid.UUID(tenant.Bytes).String(), uuid.UUID(id.Bytes).String())
	if err != nil {
		return SavedAgent{}, nil, ErrCredentialStorageUnavailable
	}
	var provider v1.ModelProviderInput
	if json.Unmarshal(raw, &provider) != nil || provider.Validate() != nil {
		return SavedAgent{}, nil, ErrCredentialStorageUnavailable
	}
	return agent, &provider, nil
}

func validateAgentModelExecution(configuration []byte, provider *v1.ModelProviderInput) error {
	if provider != nil {
		if err := provider.Validate(); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidInput, err)
		}
	}
	var config struct {
		Core *v1.SavedAgentCore `json:"x_agents_core"`
	}
	if err := json.Unmarshal(configuration, &config); err != nil {
		return ErrInvalidInput
	}
	if config.Core != nil {
		if err := v1.ValidateHarnessConfig(config.Core.Harness, config.Core.HarnessConfig); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidInput, err)
		}
	}
	if config.Core == nil || config.Core.ModelProvider == nil || config.Core.Harness == "" {
		return nil
	}
	if err := config.Core.ModelProvider.ValidateHarness(config.Core.Harness); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidInput, err)
	}
	return nil
}

func mergeAgentConfiguration(configuration, patch map[string]json.RawMessage) error {
	_, modelChanged := patch["model"]
	var corePatch map[string]json.RawMessage
	if raw := patch["x_agents_core"]; len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &corePatch); err != nil {
			return err
		}
	}
	_, providerChanged := corePatch["model_provider"]
	_, harnessChanged := corePatch["harness"]
	_, nativeSupplied := corePatch["harness_config"]
	if (modelChanged || providerChanged || harnessChanged) && !nativeSupplied && string(patch["x_agents_core"]) != "null" {
		if corePatch == nil {
			corePatch = map[string]json.RawMessage{}
		}
		corePatch["harness_config"] = json.RawMessage(`{}`)
		raw, err := json.Marshal(corePatch)
		if err != nil {
			return err
		}
		patch["x_agents_core"] = raw
	}

	for field, value := range patch {
		if field == "x_agents_core" && string(value) != "null" {
			core := map[string]json.RawMessage{}
			if old := configuration[field]; len(old) != 0 && string(old) != "null" {
				if err := json.Unmarshal(old, &core); err != nil {
					return err
				}
			}
			var changes map[string]json.RawMessage
			if err := json.Unmarshal(value, &changes); err != nil {
				return err
			}
			for key, replacement := range changes {
				core[key] = replacement
			}
			merged, err := json.Marshal(core)
			if err != nil {
				return err
			}
			value = merged
		}
		configuration[field] = value
	}
	return nil
}
