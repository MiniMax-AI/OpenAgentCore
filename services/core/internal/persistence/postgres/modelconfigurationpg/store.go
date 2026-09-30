// Package modelconfigurationpg stores deployment default model configurations
// in PostgreSQL.
package modelconfigurationpg

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
)

// auditResource is the administrator audit resource type of a deployment
// default; its ID is the Harness.
const auditResource = "deployment_model_provider"

// observationBudget bounds one observation, including its wait for a pooled
// connection and for the default's row lock.
const observationBudget = time.Second

// Store keeps deployment defaults on pooled connections and seals and opens
// their bundles. It never uses the execution lease.
type Store struct {
	pool   *pgunit.Pool
	cipher *credentialcrypto.Cipher
}

var (
	_ modelconfiguration.Storage  = (*Store)(nil)
	_ modelconfiguration.Reader   = (*Store)(nil)
	_ modelconfiguration.Observer = (*Store)(nil)
)

// New returns a Store. Without a credential key (cipher nil), Replace and
// LoadBundle fail with credentialcrypto.ErrUnavailable; List, Delete and
// observations keep working.
func New(pool *pgunit.Pool, cipher *credentialcrypto.Cipher) *Store {
	return &Store{pool: pool, cipher: cipher}
}

// List reads every default in Harness order without its sealed bundle.
func (s *Store) List(ctx context.Context) ([]modelconfiguration.Configuration, error) {
	rows, err := s.pool.Queries().ListDeploymentModelProviders(ctx)
	if err != nil {
		return nil, translate(err)
	}
	result := make([]modelconfiguration.Configuration, 0, len(rows))
	for _, row := range rows {
		result = append(result, configuration(row))
	}
	return result, nil
}

// Replace seals the complete bundle to its Harness, upserts the record under a
// new revision, which clears the replaced revision's observations, and audits
// the write in the same transaction.
func (s *Store) Replace(ctx context.Context, record modelconfiguration.Record) (modelconfiguration.Configuration, error) {
	if s.cipher == nil {
		return modelconfiguration.Configuration{}, credentialcrypto.ErrUnavailable
	}
	raw, err := json.Marshal(record.Configuration)
	if err != nil {
		return modelconfiguration.Configuration{}, err
	}
	sealed, err := s.cipher.SealDeploymentModelProvider(raw, record.Harness)
	if err != nil {
		return modelconfiguration.Configuration{}, credentialcrypto.ErrUnavailable
	}
	var result modelconfiguration.Configuration
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := q.UpsertDeploymentModelProvider(ctx, sqlc.UpsertDeploymentModelProviderParams{
			Harness: record.Harness, Protocol: record.Provider.Protocol, BaseUrl: record.Provider.BaseURL,
			ContextWindow: record.Provider.ContextWindow, MaxOutputTokens: record.Provider.MaxOutputTokens,
			Model: record.Model, HarnessConfig: record.HarnessConfig, EncryptedConfig: sealed,
			Revision: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		})
		if err != nil {
			return err
		}
		result = configuration(sqlc.ListDeploymentModelProvidersRow(row))
		return auditpg.RecordDeploymentMutation(ctx, q, "set", auditResource, record.Harness)
	})
	if err != nil {
		return modelconfiguration.Configuration{}, translate(err)
	}
	return result, nil
}

// Delete removes the Harness's default, if any, and audits the request in the
// same transaction.
func (s *Store) Delete(ctx context.Context, harness string) error {
	return translate(s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.DeleteDeploymentModelProvider(ctx, harness); err != nil {
			return err
		}
		return auditpg.RecordDeploymentMutation(ctx, q, "delete", auditResource, harness)
	}))
}

// LoadBundle reads the sealed bundle and its revision in one statement, so the
// pair always belongs to the same replacement, and opens the bundle.
func (s *Store) LoadBundle(ctx context.Context, harness string) (modelconfiguration.Bundle, error) {
	row, err := s.pool.Queries().GetDeploymentModelProviderSecret(ctx, harness)
	if errors.Is(err, pgx.ErrNoRows) {
		return modelconfiguration.Bundle{}, modelconfiguration.ErrNotFound
	}
	if err != nil {
		return modelconfiguration.Bundle{}, translate(err)
	}
	if s.cipher == nil {
		return modelconfiguration.Bundle{}, credentialcrypto.ErrUnavailable
	}
	raw, err := s.cipher.OpenDeploymentModelProvider(row.EncryptedConfig, harness)
	if err != nil {
		return modelconfiguration.Bundle{}, errors.New("deployment model configuration decryption failed")
	}
	var configuration v1.ModelConfigurationInput
	if json.Unmarshal(raw, &configuration) != nil {
		return modelconfiguration.Bundle{}, errors.New("invalid stored deployment model configuration")
	}
	return modelconfiguration.Bundle{Configuration: configuration, Revision: uuid.UUID(row.Revision.Bytes)}, nil
}

// ObserveDeploymentModelProvider runs one metadata UPDATE in its own pooled
// transaction. The statement reads only committed state: the tenant's root
// Turn, its outcome, the Session's deployment source and the exact frozen
// revision. It locks only the matching default and samples the receipt time
// after that lock. The transaction sets server-side statement and lock
// timeouts, because client cancellation alone can leave PostgreSQL executing
// briefly after pgx has returned a deadline error.
func (s *Store) ObserveDeploymentModelProvider(ctx context.Context, observation modelconfiguration.Observation) (int64, error) {
	params, err := observationParams(observation)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, observationBudget)
	defer cancel()
	var count int64
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		deadline, _ := ctx.Deadline()
		// Leave a small part of the budget for returning the server error and
		// releasing the transaction before the client deadline.
		timeout := time.Until(deadline).Milliseconds() - 25
		if timeout <= 0 {
			return context.DeadlineExceeded
		}
		setting := strconv.FormatInt(timeout, 10) + "ms"
		if _, err := tx.Exec(ctx, "SELECT set_config('statement_timeout', $1, true), set_config('lock_timeout', $1, true)", setting); err != nil {
			return err
		}
		var err error
		count, err = sqlc.New(tx).ObserveDeploymentModelProvider(ctx, params)
		return err
	})
	if err != nil {
		return 0, translate(err)
	}
	return count, nil
}

func observationParams(observation modelconfiguration.Observation) (sqlc.ObserveDeploymentModelProviderParams, error) {
	tenant, tenantErr := pgunit.ParseID(observation.TenantID)
	session, sessionErr := pgunit.ParseID(observation.SessionID)
	turn, turnErr := pgunit.ParseID(observation.TurnID)
	if tenantErr != nil || sessionErr != nil || turnErr != nil {
		return sqlc.ObserveDeploymentModelProviderParams{}, modelconfiguration.ErrInvalidObservation
	}
	return sqlc.ObserveDeploymentModelProviderParams{TenantID: tenant, SessionID: session, TurnID: turn}, nil
}

// configuration maps a stored row to the safe view. A stored default always
// holds a provider key.
func configuration(row sqlc.ListDeploymentModelProvidersRow) modelconfiguration.Configuration {
	result := modelconfiguration.Configuration{
		Harness: row.Harness, Model: row.Model, HarnessConfig: json.RawMessage(row.HarnessConfig), UpdatedAt: row.UpdatedAt.Time,
		LastUsedAt: timestamp(row.LastUsedAt), LastErrorAt: timestamp(row.LastErrorAt),
		Provider: v1.ModelProviderView{Protocol: row.Protocol, BaseURL: row.BaseUrl, ContextWindow: row.ContextWindow, MaxOutputTokens: row.MaxOutputTokens, APIKeyConfigured: true},
	}
	if row.LastErrorCode.Valid {
		result.LastErrorCode = &row.LastErrorCode.String
	}
	return result
}

func timestamp(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

// translate maps text PostgreSQL cannot store to the shared error. Every
// other failure, including an invalid audit source, passes through unchanged.
func translate(err error) error {
	if pgunit.IsUnstorableText(err) {
		return textvalue.ErrUnstorable
	}
	return err
}
