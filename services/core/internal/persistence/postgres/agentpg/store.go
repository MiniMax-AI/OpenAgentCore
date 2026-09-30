// Package agentpg stores saved Agents in PostgreSQL. An Agent's model provider
// bundle is sealed with the Core credential key, bound to its tenant and
// Agent, in the Agent's transaction.
package agentpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/textvalue"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Store struct {
	pool   *pgunit.Pool
	cipher *credentialcrypto.Cipher
}

// New returns the Agent store. cipher is nil when Core has no credential key;
// saving or opening a model provider bundle then fails with
// credentialcrypto.ErrUnavailable, as does opening a bundle the key cannot open.
func New(pool *pgunit.Pool, cipher *credentialcrypto.Cipher) *Store {
	return &Store{pool: pool, cipher: cipher}
}

var (
	_ agents.Storage = (*Store)(nil)
	_ agents.Reader  = (*Store)(nil)
)

func (s *Store) CreateAgent(ctx context.Context, input agents.NewAgent) (agents.Agent, error) {
	tenant, err := parseTenant(input.TenantID)
	if err != nil {
		return agents.Agent{}, err
	}
	var created agents.Agent
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := q.CreateAgent(ctx, sqlc.CreateAgentParams{
			ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: tenant,
			Metadata: input.Metadata, Configuration: input.Configuration,
		})
		if err != nil {
			return err
		}
		if input.ModelProvider != nil {
			if err := s.saveModelProvider(ctx, q, row.TenantID, row.ID, input.ModelProvider); err != nil {
				return err
			}
		}
		if created, err = agentFromRow(row); err != nil {
			return err
		}
		return auditpg.RecordWriteAudit(ctx, q, input.TenantID, "create", "agent", created.ID, "", writeaudit.Resource{Type: "agent", ID: created.ID})
	})
	if err != nil {
		return agents.Agent{}, translate(err)
	}
	return created, nil
}

// WithAgentUpdate treats an agentID that cannot name an Agent as a missing one.
func (s *Store) WithAgentUpdate(ctx context.Context, tenantID, agentID string, update func(agents.UpdateTx) error) error {
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return err
	}
	return s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return update(&updateTx{ctx: ctx, q: sqlc.New(tx), store: s, tenantID: tenantID, tenant: tenant, id: pgunit.PathID(agentID)})
	})
}

// updateTx runs on the transaction's context, which bounds every statement.
type updateTx struct {
	ctx      context.Context
	q        *sqlc.Queries
	store    *Store
	tenantID string
	tenant   pgtype.UUID
	id       pgtype.UUID
}

func (t *updateTx) LoadAgent() (agents.Agent, error) {
	row, err := t.q.LockAgent(t.ctx, sqlc.LockAgentParams{TenantID: t.tenant, ID: t.id})
	if errors.Is(err, pgx.ErrNoRows) {
		return agents.Agent{}, agents.ErrNotFound
	}
	if err != nil {
		return agents.Agent{}, err
	}
	return agentFromRow(row)
}

func (t *updateTx) ApplyRevision(revision agents.Revision) (agents.Agent, error) {
	updated, err := t.apply(revision)
	if err != nil {
		return agents.Agent{}, translate(err)
	}
	return updated, nil
}

func (t *updateTx) apply(revision agents.Revision) (agents.Agent, error) {
	if change := revision.ModelProvider; change != nil {
		if change.Provider == nil {
			if err := t.q.DeleteAgentModelExecution(t.ctx, t.id); err != nil {
				return agents.Agent{}, err
			}
		} else if err := t.store.saveModelProvider(t.ctx, t.q, t.tenant, t.id, change.Provider); err != nil {
			return agents.Agent{}, err
		}
	}
	row, err := t.q.UpdateAgent(t.ctx, sqlc.UpdateAgentParams{TenantID: t.tenant, ID: t.id, Configuration: revision.Configuration, Metadata: revision.Metadata})
	if errors.Is(err, pgx.ErrNoRows) {
		return agents.Agent{}, agents.ErrNotFound
	}
	if err != nil {
		return agents.Agent{}, err
	}
	updated, err := agentFromRow(row)
	if err != nil {
		return agents.Agent{}, err
	}
	return updated, auditpg.RecordWriteAudit(t.ctx, t.q, t.tenantID, "update", "agent", updated.ID, "")
}

// DeleteAgent treats an agentID that cannot name an Agent as a missing one.
// The model provider bundle goes with the Agent by foreign-key cascade.
func (s *Store) DeleteAgent(ctx context.Context, tenantID, agentID string) (string, error) {
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return "", err
	}
	var deleted string
	err = s.pool.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		id, err := q.DeleteAgent(ctx, sqlc.DeleteAgentParams{TenantID: tenant, ID: pgunit.PathID(agentID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return agents.ErrNotFound
		}
		if err != nil {
			return err
		}
		deleted = uuid.UUID(id.Bytes).String()
		return auditpg.RecordWriteAudit(ctx, q, tenantID, "delete", "agent", deleted, "")
	})
	if err != nil {
		return "", err
	}
	return deleted, nil
}

// GetAgent treats an agentID that cannot name an Agent as a missing one.
func (s *Store) GetAgent(ctx context.Context, tenantID, agentID string) (agents.Agent, error) {
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return agents.Agent{}, err
	}
	row, err := s.pool.Queries().GetAgent(ctx, sqlc.GetAgentParams{TenantID: tenant, ID: pgunit.PathID(agentID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return agents.Agent{}, agents.ErrNotFound
	}
	if err != nil {
		return agents.Agent{}, err
	}
	return agentFromRow(row)
}

// ListAgents reads the cursor Agent and the page from one snapshot. A cursor
// that cannot name an Agent is a missing one.
func (s *Store) ListAgents(ctx context.Context, query agents.ListQuery) (agents.Page, error) {
	if err := query.Validate(); err != nil {
		return agents.Page{}, err
	}
	tenant, err := parseTenant(query.TenantID)
	if err != nil {
		return agents.Page{}, err
	}
	var page agents.Page
	err = s.pool.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		params := sqlc.ListAgentsParams{TenantID: tenant, PageLimit: int32(query.Limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: query.Ascending}
		if query.After != "" {
			after, err := q.GetAgent(ctx, sqlc.GetAgentParams{TenantID: tenant, ID: pgunit.PathID(query.After)})
			if errors.Is(err, pgx.ErrNoRows) {
				return agents.ErrNotFound
			}
			if err != nil {
				return err
			}
			params.AfterCreated, params.AfterID = after.CreatedAt, after.ID
		}
		rows, err := q.ListAgents(ctx, params)
		if err != nil {
			return err
		}
		page = agents.Page{Agents: make([]agents.Agent, 0, min(query.Limit, len(rows)))}
		if len(rows) > query.Limit {
			page.NextCursor = uuid.UUID(rows[query.Limit-1].ID.Bytes).String()
			rows = rows[:query.Limit]
		}
		for _, row := range rows {
			agent, err := agentFromRow(row)
			if err != nil {
				return err
			}
			page.Agents = append(page.Agents, agent)
		}
		return nil
	})
	if err != nil {
		return agents.Page{}, err
	}
	return page, nil
}

// GetAgentWithModelProvider reads the Agent and its sealed bundle with one
// statement and opens the bundle. An agentID that cannot name an Agent is a
// missing one.
func (s *Store) GetAgentWithModelProvider(ctx context.Context, tenantID, agentID string) (agents.Agent, *v1.ModelProviderInput, error) {
	tenant, err := parseTenant(tenantID)
	if err != nil {
		return agents.Agent{}, nil, err
	}
	row, err := s.pool.Queries().GetAgentWithModelExecution(ctx, sqlc.GetAgentWithModelExecutionParams{TenantID: tenant, AgentID: pgunit.PathID(agentID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return agents.Agent{}, nil, agents.ErrNotFound
	}
	if err != nil {
		return agents.Agent{}, nil, err
	}
	agent, err := agentFromRow(sqlc.Agent{ID: row.ID, TenantID: row.TenantID, Metadata: row.Metadata, Configuration: row.Configuration, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	if err != nil {
		return agents.Agent{}, nil, err
	}
	if row.EncryptedConfig == nil {
		return agent, nil, nil
	}
	raw, err := s.cipher.OpenAgentModelExecution(row.EncryptedConfig, agent.TenantID, agent.ID)
	if err != nil {
		return agents.Agent{}, nil, credentialcrypto.ErrUnavailable
	}
	var provider v1.ModelProviderInput
	if json.Unmarshal(raw, &provider) != nil || provider.Validate() != nil {
		return agents.Agent{}, nil, credentialcrypto.ErrUnavailable
	}
	return agent, &provider, nil
}

func (s *Store) saveModelProvider(ctx context.Context, q *sqlc.Queries, tenant, agent pgtype.UUID, provider *v1.ModelProviderInput) error {
	raw, err := json.Marshal(provider)
	if err != nil {
		return err
	}
	sealed, err := s.cipher.SealAgentModelExecution(raw, uuid.UUID(tenant.Bytes).String(), uuid.UUID(agent.Bytes).String())
	if err != nil {
		return credentialcrypto.ErrUnavailable
	}
	return q.SaveAgentModelExecution(ctx, sqlc.SaveAgentModelExecutionParams{AgentID: agent, EncryptedConfig: sealed})
}

func parseTenant(value string) (pgtype.UUID, error) {
	tenant, err := pgunit.ParseID(value)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("%w: tenant ID", agents.ErrInvalidInput)
	}
	return tenant, nil
}

func agentFromRow(row sqlc.Agent) (agents.Agent, error) {
	agent := agents.Agent{
		ID: uuid.UUID(row.ID.Bytes).String(), TenantID: uuid.UUID(row.TenantID.Bytes).String(),
		Configuration: row.Configuration, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
	if err := json.Unmarshal(row.Metadata, &agent.Metadata); err != nil {
		return agents.Agent{}, fmt.Errorf("decode agent metadata: %w", err)
	}
	return agent, nil
}

// translate reports text PostgreSQL cannot store as textvalue.ErrUnstorable
// and returns any other error as it is.
func translate(err error) error {
	if pgunit.IsUnstorableText(err) {
		return textvalue.ErrUnstorable
	}
	return err
}
