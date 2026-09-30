package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrModelProviderRequired reports a hosted or self-hosted Session that has no
// frozen model provider and therefore cannot run.
var ErrModelProviderRequired = errors.New("the Session has no model provider")

// DeploymentModelProvider is the safe view of one harness's deployment default.
// The API key is write-only; only its presence is reported.
type DeploymentModelProvider struct {
	Harness       string
	Provider      v1.ModelProviderView
	Model         string
	HarnessConfig json.RawMessage
	UpdatedAt     time.Time
	LastUsedAt    *time.Time
	LastErrorCode *string
	LastErrorAt   *time.Time
}

func deploymentModelProvider(harness, protocol, baseURL string, contextWindow, maxOutputTokens int32, model string, harnessConfig json.RawMessage, updatedAt time.Time, lastUsedAt, lastErrorAt pgtype.Timestamptz, lastErrorCode pgtype.Text) DeploymentModelProvider {
	var code *string
	if lastErrorCode.Valid {
		code = &lastErrorCode.String
	}
	return DeploymentModelProvider{Harness: harness, Model: model, HarnessConfig: harnessConfig, UpdatedAt: updatedAt, LastUsedAt: resetTimestamp(lastUsedAt), LastErrorAt: resetTimestamp(lastErrorAt), LastErrorCode: code, Provider: v1.ModelProviderView{
		Protocol: protocol, BaseURL: baseURL, ContextWindow: contextWindow, MaxOutputTokens: maxOutputTokens, APIKeyConfigured: true,
	}}
}

// ListDeploymentModelProviders reads the configured defaults without decryption.
func (s *Store) ListDeploymentModelProviders(ctx context.Context) ([]DeploymentModelProvider, error) {
	rows, err := s.queries.ListDeploymentModelProviders(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]DeploymentModelProvider, 0, len(rows))
	for _, row := range rows {
		result = append(result, deploymentModelProvider(row.Harness, row.Protocol, row.BaseUrl, row.ContextWindow, row.MaxOutputTokens, row.Model, row.HarnessConfig, row.UpdatedAt.Time, row.LastUsedAt, row.LastErrorAt, row.LastErrorCode))
	}
	return result, nil
}

// SetDeploymentModelProvider replaces a harness's complete default bundle and
// audits the write in the same transaction, without the key.
func (s *Store) SetDeploymentModelProvider(ctx context.Context, harness string, configuration v1.ModelConfigurationInput) (DeploymentModelProvider, error) {
	if err := configuration.ValidateHarness(harness); err != nil {
		return DeploymentModelProvider{}, fmt.Errorf("%w: %s", ErrInvalidInput, err)
	}
	provider := configuration.ModelProvider
	raw, err := json.Marshal(configuration)
	if err != nil {
		return DeploymentModelProvider{}, err
	}
	encrypted, err := s.credentialCipher.SealDeploymentModelProvider(raw, harness)
	if err != nil {
		return DeploymentModelProvider{}, credentialcrypto.ErrUnavailable
	}
	var result DeploymentModelProvider
	err = s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		row, err := q.UpsertDeploymentModelProvider(ctx, sqlc.UpsertDeploymentModelProviderParams{
			Harness: harness, Protocol: provider.Protocol, BaseUrl: provider.BaseURL, Model: configuration.Model, HarnessConfig: v1.ResolvedHarnessConfig(configuration.HarnessConfig),
			ContextWindow: provider.ContextWindow, MaxOutputTokens: provider.MaxOutputTokens, EncryptedConfig: encrypted, Revision: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		})
		if err != nil {
			return err
		}
		result = deploymentModelProvider(row.Harness, row.Protocol, row.BaseUrl, row.ContextWindow, row.MaxOutputTokens, row.Model, row.HarnessConfig, row.UpdatedAt.Time, row.LastUsedAt, row.LastErrorAt, row.LastErrorCode)
		return auditpg.RecordDeploymentMutation(ctx, q, "set", "deployment_model_provider", harness)
	})
	return result, err
}

// DeleteDeploymentModelProvider is idempotent; each successful call is audited.
// Sessions that already froze the default keep their snapshot.
func (s *Store) DeleteDeploymentModelProvider(ctx context.Context, harness string) error {
	return s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if _, err := q.DeleteDeploymentModelProvider(ctx, harness); err != nil {
			return err
		}
		return auditpg.RecordDeploymentMutation(ctx, q, "delete", "deployment_model_provider", harness)
	})
}

// DeploymentModelProvider decrypts a harness's default for Session creation. It
// returns nil when none is configured and fails closed when the bundle cannot
// be decrypted.
func (s *Store) DeploymentModelProvider(ctx context.Context, harness string) (*DeploymentModelProviderSnapshot, error) {
	snapshot, err := s.queries.GetDeploymentModelProviderSecret(ctx, harness)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	raw, err := s.credentialCipher.OpenDeploymentModelProvider(snapshot.EncryptedConfig, harness)
	if err != nil {
		return nil, credentialcrypto.ErrUnavailable
	}
	var configuration v1.ModelConfigurationInput
	if json.Unmarshal(raw, &configuration) != nil {
		return nil, credentialcrypto.ErrUnavailable
	}
	// A decrypted but unsupported configuration is not a credential failure.
	// Keep its stored snapshot intact so the operator can inspect and replace it.
	if err := configuration.ValidateHarness(harness); err != nil {
		return nil, err
	}
	return &DeploymentModelProviderSnapshot{Provider: &configuration.ModelProvider, Model: configuration.Model, HarnessConfig: v1.ResolvedHarnessConfig(configuration.HarnessConfig), Revision: uuid.UUID(snapshot.Revision.Bytes)}, nil
}

// DeploymentModelProviderSnapshot pairs one decrypted bundle with its private
// revision from the same database read. It never enters a public projection.
type DeploymentModelProviderSnapshot struct {
	Provider      *v1.ModelProviderInput
	Model         string
	HarnessConfig json.RawMessage
	Revision      uuid.UUID `json:"-"`
}
