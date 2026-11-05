package sessionpg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

var _ sessions.StandaloneCreationTx = (*standaloneCreationTx)(nil)

func (s *Store) WithEnvironmentCreation(ctx context.Context, tenantID string, ident sessions.EnvironmentIdentity, apply func(context.Context, sessions.StandaloneCreationTx) error) error {
	tenant, err := parseID(tenantID)
	if err != nil {
		return err
	}
	return s.units.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		base := &creationTx{SessionTx: SessionTx{q: sqlc.New(tx), tenant: tenant}, tenantID: tenantID, tx: tx, cipher: s.cipher}
		return apply(ctx, &standaloneCreationTx{creationTx: base, identity: ident})
	})
}

func (s *Store) FindEnvironmentCreation(ctx context.Context, tenantID, key string) (sessions.EnvironmentCreationRecord, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.EnvironmentCreationRecord{}, err
	}
	return findEnvironmentCreation(ctx, s.units.Queries(), tenant, key)
}

func findEnvironmentCreation(ctx context.Context, q *sqlc.Queries, tenant pgtype.UUID, key string) (sessions.EnvironmentCreationRecord, error) {
	row, err := q.FindEnvironmentCreation(ctx, sqlc.FindEnvironmentCreationParams{TenantID: tenant, IdempotencyKey: pgtype.Text{String: key, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.EnvironmentCreationRecord{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.EnvironmentCreationRecord{}, err
	}
	return sessions.EnvironmentCreationRecord{EnvironmentID: optionalID(row.ID), EnvironmentIdentity: sessions.EnvironmentIdentity{
		Key: row.IdempotencyKey.String, Hash: row.RequestHash.String, Creator: identity.Subject{Kind: row.CreatorKind.String, ID: row.CreatorID.String},
	}}, nil
}

// standaloneCreationTx creates an Environment no Session owns, under its
// creation identity.
type standaloneCreationTx struct {
	*creationTx
	identity sessions.EnvironmentIdentity
}

func (t *standaloneCreationTx) FindCreation(ctx context.Context) (sessions.EnvironmentCreationRecord, error) {
	if err := t.q.LockEnvironmentCreation(ctx, sqlc.LockEnvironmentCreationParams{TenantID: t.tenantID, IdempotencyKey: t.identity.Key}); err != nil {
		return sessions.EnvironmentCreationRecord{}, err
	}
	return findEnvironmentCreation(ctx, t.q, t.tenant, t.identity.Key)
}

func (t *standaloneCreationTx) CreateEnvironment(ctx context.Context, configuration json.RawMessage, prepared bool) (string, error) {
	err := t.insertEnvironment(ctx, sqlc.CreateEnvironmentParams{
		Configuration: configuration, Prepared: prepared,
		IdempotencyKey: pgtype.Text{String: t.identity.Key, Valid: true}, RequestHash: pgtype.Text{String: t.identity.Hash, Valid: true},
		CreatorKind: pgtype.Text{String: t.identity.Creator.Kind, Valid: true}, CreatorID: pgtype.Text{String: t.identity.Creator.ID, Valid: true},
	})
	return optionalID(t.environment), storable(err)
}

func (t *standaloneCreationTx) AuditCreation(ctx context.Context, environment string, created ...writeaudit.Resource) error {
	return auditpg.RecordWriteAudit(ctx, t.q, t.tenantID, writeaudit.ActionCreate, writeaudit.ResourceEnvironment, environment, "", created...)
}
