// Package workspacepg stores independent workspace configurations and durable
// Environment filesystem ownership. Execution writes use Core's existing lease.
package workspacepg

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspaces"
)

type Store struct{ pool *pgunit.Pool }

func New(pool *pgunit.Pool) *Store { return &Store{pool: pool} }

var _ workspaces.Storage = (*Store)(nil)

func (s *Store) ActiveConfiguration(ctx context.Context) (workspacefs.Configuration, error) {
	row, err := s.pool.Queries().GetActiveWorkspaceConfiguration(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return workspacefs.Configuration{}, workspaces.ErrNotConfigured
	}
	if err != nil {
		return workspacefs.Configuration{}, err
	}
	return configuration(row), nil
}

// SelectConfiguration atomically changes the selection without changing any
// configuration's definition or any existing Environment's binding.
func (e *Execution) SelectConfiguration(ctx context.Context, config workspacefs.Configuration) error {
	if err := config.Validate(); err != nil {
		return err
	}
	id := pgunit.PathID(config.ID)
	return e.lease.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := q.LockWorkspaceConfigurations(ctx); err != nil {
			return err
		}
		if err := q.InsertWorkspaceConfiguration(ctx, sqlc.InsertWorkspaceConfigurationParams{ID: id, Adapter: config.Adapter, Parameters: config.Parameters}); err != nil {
			return err
		}
		matches, err := q.MatchWorkspaceConfiguration(ctx, sqlc.MatchWorkspaceConfigurationParams{ID: id, Adapter: config.Adapter, Parameters: config.Parameters})
		if err != nil {
			return err
		}
		if !matches {
			return workspaces.ErrConflict
		}
		if err := q.ClearActiveWorkspaceConfiguration(ctx); err != nil {
			return err
		}
		if err := q.ActivateWorkspaceConfiguration(ctx, id); err != nil {
			return err
		}
		return auditpg.RecordDeploymentMutation(ctx, q, "change", "workspace_storage", config.ID)
	})
}

func (s *Store) Get(ctx context.Context, tenant, environment string) (workspaces.Record, error) {
	return get(ctx, s.pool.Queries(), tenant, environment)
}

func (s *Store) DeletionCandidates(ctx context.Context, after string) ([]workspaces.Record, error) {
	cursor := pgtype.UUID{Valid: true}
	if after != "" {
		var err error
		cursor, err = pgunit.ParseID(after)
		if err != nil {
			return nil, err
		}
	}
	rows, err := s.pool.Queries().ListWorkspaceDeletionCandidates(ctx, cursor)
	if err != nil {
		return nil, err
	}
	result := make([]workspaces.Record, 0, len(rows))
	for _, row := range rows {
		record, err := record(row.EnvironmentWorkspace, row.WorkspaceFsConfiguration, row.TenantID)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

type Execution struct{ lease *pgunit.Lease }

func NewExecution(lease *pgunit.Lease) *Execution { return &Execution{lease: lease} }

var _ workspaces.ExecutionStorage = (*Execution)(nil)

func (e *Execution) Bind(ctx context.Context, tenant, environment string) (workspaces.Record, error) {
	var result workspaces.Record
	err := e.lease.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		deleted, err := lockSession(ctx, q, tenant, environment)
		if err != nil {
			return err
		}
		if deleted {
			return workspaces.ErrConflict
		}
		result, err = get(ctx, q, tenant, environment)
		if err == nil {
			if result.State == workspaces.Deleting || result.State == workspaces.Deleted {
				return workspaces.ErrConflict
			}
			return nil
		}
		if !errors.Is(err, workspaces.ErrNotFound) {
			return err
		}
		config, err := q.GetActiveWorkspaceConfiguration(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return workspaces.ErrNotConfigured
		}
		if err != nil {
			return err
		}
		if err := q.InsertEnvironmentWorkspace(ctx, sqlc.InsertEnvironmentWorkspaceParams{
			ObjectID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, EnvironmentID: pgunit.PathID(environment), ConfigurationID: config.ID,
		}); err != nil {
			return err
		}
		result, err = get(ctx, q, tenant, environment)
		return err
	})
	if err != nil {
		return workspaces.Record{}, err
	}
	return result, nil
}

func (e *Execution) MarkReady(ctx context.Context, ref workspacefs.Reference, attachment workspacefs.Attachment) error {
	return e.change(ctx, ref, func(ctx context.Context, q *sqlc.Queries, current workspaces.Record, deleted bool) error {
		if deleted || current.State != workspaces.Creating {
			return workspaces.ErrConflict
		}
		if err := workspacefs.ValidateAttachment(ref, current.Configuration, attachment); err != nil {
			return err
		}
		raw, err := json.Marshal(attachment)
		if err != nil {
			return err
		}
		changed, err := q.MarkWorkspaceReady(ctx, sqlc.MarkWorkspaceReadyParams{ObjectID: pgunit.PathID(ref.ObjectID), Attachment: raw})
		return changedOne(changed, err)
	})
}

func (e *Execution) BeginDelete(ctx context.Context, ref workspacefs.Reference) error {
	return e.change(ctx, ref, func(ctx context.Context, q *sqlc.Queries, current workspaces.Record, deleted bool) error {
		if !deleted {
			return workspaces.ErrConflict
		}
		if current.State == workspaces.Deleted {
			return nil
		}
		// runtime_allocations owns both compute and retained snapshots in
		// compute_state. Its released receipt follows cleanup of both. A missing
		// allocation also covers deletion before compute admission ever occurred.
		released, err := q.WorkspaceComputeReleased(ctx, pgunit.PathID(ref.EnvironmentID))
		if err != nil {
			return err
		}
		if !released {
			return workspaces.ErrConflict
		}
		if current.State == workspaces.Deleting {
			return nil
		}
		changed, err := q.BeginWorkspaceDeletion(ctx, pgunit.PathID(ref.ObjectID))
		return changedOne(changed, err)
	})
}

func (e *Execution) MarkDeleted(ctx context.Context, ref workspacefs.Reference) error {
	return e.change(ctx, ref, func(ctx context.Context, q *sqlc.Queries, current workspaces.Record, _ bool) error {
		if current.State == workspaces.Deleted {
			return nil
		}
		changed, err := q.MarkWorkspaceDeleted(ctx, pgunit.PathID(ref.ObjectID))
		return changedOne(changed, err)
	})
}

func (e *Execution) change(ctx context.Context, ref workspacefs.Reference, apply func(context.Context, *sqlc.Queries, workspaces.Record, bool) error) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	return e.lease.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		deleted, err := lockSession(ctx, q, ref.TenantID, ref.EnvironmentID)
		if err != nil {
			return err
		}
		current, err := get(ctx, q, ref.TenantID, ref.EnvironmentID)
		if err != nil {
			return err
		}
		if current.Reference != ref {
			return workspaces.ErrConflict
		}
		return apply(ctx, q, current, deleted)
	})
}

func lockSession(ctx context.Context, q *sqlc.Queries, tenant, environment string) (bool, error) {
	id, err := q.LockWorkspaceSession(ctx, sqlc.LockWorkspaceSessionParams{TenantID: pgunit.PathID(tenant), ID: pgunit.PathID(environment)})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, workspaces.ErrNotFound
	}
	if err != nil {
		return false, err
	}
	// Read after acquiring the Session lock with a fresh statement snapshot.
	return q.WorkspaceSessionDeleted(ctx, id)
}

func get(ctx context.Context, q *sqlc.Queries, tenant, environment string) (workspaces.Record, error) {
	row, err := q.GetEnvironmentWorkspace(ctx, sqlc.GetEnvironmentWorkspaceParams{TenantID: pgunit.PathID(tenant), ID: pgunit.PathID(environment)})
	if errors.Is(err, pgx.ErrNoRows) {
		return workspaces.Record{}, workspaces.ErrNotFound
	}
	if err != nil {
		return workspaces.Record{}, err
	}
	return record(row.EnvironmentWorkspace, row.WorkspaceFsConfiguration, row.TenantID)
}

func configuration(row sqlc.WorkspaceFsConfiguration) workspacefs.Configuration {
	return workspacefs.Configuration{ID: uuid.UUID(row.ID.Bytes).String(), Adapter: row.Adapter, Parameters: row.Parameters}
}

func record(row sqlc.EnvironmentWorkspace, config sqlc.WorkspaceFsConfiguration, tenant pgtype.UUID) (workspaces.Record, error) {
	result := workspaces.Record{
		Reference:     workspacefs.Reference{TenantID: uuid.UUID(tenant.Bytes).String(), EnvironmentID: uuid.UUID(row.EnvironmentID.Bytes).String(), ObjectID: uuid.UUID(row.ObjectID.Bytes).String()},
		Configuration: configuration(config), State: workspaces.State(row.State),
	}
	if row.Attachment != nil {
		var attachment workspacefs.Attachment
		if err := json.Unmarshal(row.Attachment, &attachment); err != nil {
			return workspaces.Record{}, err
		}
		if err := workspacefs.ValidateAttachment(result.Reference, result.Configuration, attachment); err != nil {
			return workspaces.Record{}, err
		}
		result.Attachment = &attachment
	}
	return result, nil
}

func changedOne(changed int64, err error) error {
	if err != nil {
		return err
	}
	if changed != 1 {
		return workspaces.ErrConflict
	}
	return nil
}
