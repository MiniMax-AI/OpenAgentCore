package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// The projection is creation metadata, excluded from retry identity. Only the
// transaction that inserts the Session writes it; retries cannot repair or alter it.
func saveSessionExecutionConfiguration(ctx context.Context, q *sqlc.Queries, session sqlc.Session, projection *v1.SessionExecutionConfiguration, provider *v1.ModelProviderInput, providerSource string, revision uuid.UUID) error {
	if projection == nil {
		return nil
	}
	var frozenRevision pgtype.UUID
	frozen := *projection
	model, err := sessionExecutionModel(session.Configuration)
	if err != nil {
		return err
	}
	if !sameExecutionValue(frozen.Model.Value, model) || frozen.Harness.Value == nil || *frozen.Harness.Value != session.Engine ||
		!validExecutionSource(frozen.Model.Source) || !validExecutionSource(frozen.Harness.Source) {
		return fmt.Errorf("%w: execution projection does not match Session configuration", ErrInvalidInput)
	}
	switch frozen.ModelProvider.Source {
	case "deployment":
		if provider == nil {
			return fmt.Errorf("%w: execution projection has no model provider", ErrInvalidInput)
		}
		// The deployment default is readable with the same Core key, so the
		// safe view is recorded from the frozen bundle itself. Native options
		// are never part of it.
		frozen.ModelProvider.Status = "available"
		frozen.ModelProvider.Configuration = provider.SafeView()
		if providerSource == v1.ModelProviderSourceDeployment && revision != uuid.Nil {
			frozenRevision = pgtype.UUID{Bytes: revision, Valid: true}
		}
	case "session", "agent":
		if provider == nil || frozen.ModelProvider.Status != "available" || frozen.ModelProvider.Configuration == nil || *frozen.ModelProvider.Configuration != *provider.SafeView() {
			return fmt.Errorf("%w: execution projection does not match model provider", ErrInvalidInput)
		}
	case "unknown":
		frozen.ModelProvider.Status = "unavailable"
		frozen.ModelProvider.Configuration = nil
	default:
		return fmt.Errorf("%w: invalid execution projection source", ErrInvalidInput)
	}
	normalizeExecutionProjection(&frozen, uuid.UUID(session.ID.Bytes).String())
	raw, err := json.Marshal(frozen)
	if err != nil {
		return err
	}
	return q.SaveSessionExecutionConfiguration(ctx, sqlc.SaveSessionExecutionConfigurationParams{SessionID: session.ID, Configuration: raw, DeploymentProviderRevision: frozenRevision})
}

// GetSessionExecutionConfiguration reads only safe committed configuration. It
// never loads provider ciphertext, current defaults or runtime health.
func (s *Store) GetSessionExecutionConfiguration(ctx context.Context, tenantID, sessionID string) (v1.SessionExecutionConfiguration, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return v1.SessionExecutionConfiguration{}, err
	}
	row, err := s.queries.GetSessionExecutionConfiguration(ctx, sqlc.GetSessionExecutionConfigurationParams{TenantID: tenant, SessionID: pgunit.PathID(sessionID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return v1.SessionExecutionConfiguration{}, ErrNotFound
	}
	if err != nil {
		return v1.SessionExecutionConfiguration{}, fmt.Errorf("get session execution configuration: %w", err)
	}
	var projection v1.SessionExecutionConfiguration
	if len(row.ExecutionConfiguration) == 0 {
		model, err := sessionExecutionModel(row.SessionConfiguration)
		if err != nil {
			return projection, err
		}
		var harness *string
		if row.Engine != "" {
			harness = &row.Engine
		}
		projection.Model = v1.ExecutionSelection{Value: model, Source: "unknown"}
		projection.Harness = v1.ExecutionSelection{Value: harness, Source: "unknown"}
		projection.ModelProvider = v1.ExecutionProviderSelection{Source: "unknown", Status: "unavailable"}
	} else if err := json.Unmarshal(row.ExecutionConfiguration, &projection); err != nil {
		return v1.SessionExecutionConfiguration{}, errors.New("invalid stored session execution configuration")
	}
	normalizeExecutionProjection(&projection, uuid.UUID(row.ID.Bytes).String())
	return projection, nil
}

func normalizeExecutionProjection(projection *v1.SessionExecutionConfiguration, sessionID string) {
	projection.Object = "agent.session.execution_configuration"
	projection.SchemaVersion = 1
	if projection.HarnessConfig.Source == "" {
		projection.HarnessConfig.Source = "unknown"
	}
	projection.HarnessConfig.Value = v1.ResolvedHarnessConfig(projection.HarnessConfig.Value)
	projection.SessionID = sessionID
	if projection.ModelProvider.Source == "deployment" && (projection.ModelProvider.Status != "available" || projection.ModelProvider.Configuration == nil) {
		// Sessions created before deployment defaults moved into Core stay redacted.
		projection.ModelProvider.Status = "redacted"
		projection.ModelProvider.Configuration = nil
	} else if projection.ModelProvider.Status != "available" {
		projection.ModelProvider.Configuration = nil
	}
}

func sessionExecutionModel(configuration []byte) (*string, error) {
	var config struct {
		Agent struct {
			Model *string `json:"model"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(configuration, &config); err != nil {
		return nil, errors.New("invalid stored Session model configuration")
	}
	return config.Agent.Model, nil
}

func sameExecutionValue(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func validExecutionSource(source string) bool {
	switch source {
	case "session", "agent", "deployment", "unknown":
		return true
	}
	return false
}
