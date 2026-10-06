package store

import (
	"context"
	"encoding/json"
	"fmt"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
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
	model, err := sessions.ExecutionModel(session.Configuration)
	if err != nil {
		return err
	}
	if !sameExecutionValue(frozen.Model.Value, model) || frozen.Harness.Value == nil || *frozen.Harness.Value != session.Engine ||
		!validExecutionSource(frozen.Model.Source) || !validExecutionSource(frozen.Harness.Source) {
		return fmt.Errorf("%w: execution projection does not match Session configuration", sessions.ErrInvalidInput)
	}
	switch frozen.ModelProvider.Source {
	case "deployment":
		if provider == nil {
			return fmt.Errorf("%w: execution projection has no model provider", sessions.ErrInvalidInput)
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
			return fmt.Errorf("%w: execution projection does not match model provider", sessions.ErrInvalidInput)
		}
	case "unknown":
		frozen.ModelProvider.Status = "unavailable"
		frozen.ModelProvider.Configuration = nil
	default:
		return fmt.Errorf("%w: invalid execution projection source", sessions.ErrInvalidInput)
	}
	sessions.NormalizeExecutionProjection(&frozen, uuid.UUID(session.ID.Bytes).String())
	raw, err := json.Marshal(frozen)
	if err != nil {
		return err
	}
	return q.SaveSessionExecutionConfiguration(ctx, sqlc.SaveSessionExecutionConfigurationParams{SessionID: session.ID, Configuration: raw, DeploymentProviderRevision: frozenRevision})
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
