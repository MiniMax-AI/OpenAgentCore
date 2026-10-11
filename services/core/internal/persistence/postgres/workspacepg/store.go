// Package workspacepg stores independent workspace configurations and durable
// Environment filesystem ownership. Execution writes use Core's existing lease.
package workspacepg

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspaces"
)

// credentials seals adapter credentials to this installation and their
// configuration. Records are opened only for Core's Control construction.
type credentials struct {
	cipher       *credentialcrypto.Cipher
	installation string
}

func (c credentials) seal(config workspacefs.ConfigurationRecord) ([]byte, error) {
	if len(config.Credential) == 0 {
		return nil, nil
	}
	return c.cipher.SealWorkspaceConfiguration(config.Credential, c.installation, config.ID)
}

// configuration opens a retained configuration. A credential that cannot be
// authenticated for this installation and configuration is an internal error.
func (c credentials) configuration(row sqlc.WorkspaceFsConfiguration) (workspacefs.ConfigurationRecord, error) {
	record := workspacefs.ConfigurationRecord{Configuration: publicConfiguration(row)}
	if len(row.Credential) > 0 {
		var err error
		if record.Credential, err = c.cipher.OpenWorkspaceConfiguration(row.Credential, c.installation, record.ID); err != nil {
			return workspacefs.ConfigurationRecord{}, fmt.Errorf("workspace configuration credential: %w", err)
		}
	}
	return record, nil
}

type Store struct {
	pool        *pgunit.Pool
	credentials credentials
}

func New(pool *pgunit.Pool, cipher *credentialcrypto.Cipher, installation string) *Store {
	return &Store{pool: pool, credentials: credentials{cipher, installation}}
}

var _ workspaces.Storage = (*Store)(nil)

func (s *Store) ActiveConfiguration(ctx context.Context) (workspacefs.ConfigurationRecord, error) {
	row, err := s.pool.Queries().GetActiveWorkspaceConfiguration(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return workspacefs.ConfigurationRecord{}, workspaces.ErrNotConfigured
	}
	if err != nil {
		return workspacefs.ConfigurationRecord{}, err
	}
	return s.credentials.configuration(row)
}

func (s *Store) Configuration(ctx context.Context, id string) (workspacefs.ConfigurationRecord, error) {
	parsed, err := pgunit.ParseID(id)
	if err != nil {
		return workspacefs.ConfigurationRecord{}, workspacefs.ErrInvalid
	}
	row, err := s.pool.Queries().GetWorkspaceConfiguration(ctx, parsed)
	if errors.Is(err, pgx.ErrNoRows) {
		return workspacefs.ConfigurationRecord{}, workspaces.ErrNotFound
	}
	if err != nil {
		return workspacefs.ConfigurationRecord{}, err
	}
	return s.credentials.configuration(row)
}

// Selection reads the public selection and policy in one statement; it never
// opens the credential.
func (s *Store) Selection(ctx context.Context) (workspaces.Selection, error) {
	row, err := s.pool.Queries().GetWorkspaceSelection(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return workspaces.Selection{}, workspaces.ErrNotConfigured
	}
	if err != nil {
		return workspaces.Selection{}, err
	}
	return workspaces.Selection{Configuration: publicConfiguration(row.WorkspaceFsConfiguration), TenantCapacityMiB: uint32(row.TenantCapacityMib)}, nil
}

// SelectConfiguration atomically changes the selection and tenant capacity
// policy without changing any configuration's definition or any existing
// Environment's binding. The admission row lock fences concurrent Bind calls.
func (e *Execution) SelectConfiguration(ctx context.Context, config workspacefs.ConfigurationRecord, tenantCapacityMiB uint32) error {
	if err := config.Validate(); err != nil {
		return err
	}
	sealed, err := e.credentials.seal(config)
	if err != nil {
		return err
	}
	id := pgunit.PathID(config.ID)
	return e.lease.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := q.LockWorkspaceConfigurations(ctx); err != nil {
			return err
		}
		if err := q.InsertWorkspaceConfiguration(ctx, sqlc.InsertWorkspaceConfigurationParams{ID: id, Adapter: config.Adapter, Parameters: config.Parameters, Credential: sealed}); err != nil {
			return err
		}
		match, err := q.MatchWorkspaceConfiguration(ctx, sqlc.MatchWorkspaceConfigurationParams{ID: id, Adapter: config.Adapter, Parameters: config.Parameters})
		if err != nil {
			return err
		}
		var stored []byte
		if len(match.Credential) > 0 {
			if stored, err = e.credentials.cipher.OpenWorkspaceConfiguration(match.Credential, e.credentials.installation, config.ID); err != nil {
				return fmt.Errorf("workspace configuration credential: %w", err)
			}
		}
		if !match.Matches || len(stored) != len(config.Credential) || subtle.ConstantTimeCompare(stored, config.Credential) != 1 {
			return workspaces.ErrConflict
		}
		if err := q.SetWorkspaceTenantCapacity(ctx, int64(tenantCapacityMiB)); err != nil {
			return err
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
	return get(ctx, s.pool.Queries(), s.credentials, tenant, environment)
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
		record, err := record(s.credentials, row.EnvironmentWorkspace, row.WorkspaceFsConfiguration, row.TenantID)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

type Execution struct {
	lease       *pgunit.Lease
	credentials credentials
}

func NewExecution(lease *pgunit.Lease, cipher *credentialcrypto.Cipher, installation string) *Execution {
	return &Execution{lease: lease, credentials: credentials{cipher, installation}}
}

var _ workspaces.ExecutionStorage = (*Execution)(nil)

func (e *Execution) Bind(ctx context.Context, tenant, environment, configuration string, capacityMiB uint32) (workspaces.Record, error) {
	expected, err := pgunit.ParseID(configuration)
	if err != nil {
		return workspaces.Record{}, workspacefs.ErrInvalid
	}
	var result workspaces.Record
	err = e.lease.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		deleted, err := lockSession(ctx, q, tenant, environment)
		if err != nil {
			return err
		}
		if deleted {
			return workspaces.ErrConflict
		}
		result, err = get(ctx, q, e.credentials, tenant, environment)
		if err == nil {
			if result.State == workspaces.Deleting || result.State == workspaces.Deleted {
				return workspaces.ErrConflict
			}
			return nil
		}
		if !errors.Is(err, workspaces.ErrNotFound) {
			return err
		}
		// Selection changes update this row, so the policy and the selected
		// configuration below are read as one committed setting.
		tenantCapacity, err := q.LockWorkspaceAdmission(ctx)
		if err != nil {
			return err
		}
		config, err := q.GetActiveWorkspaceConfiguration(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return workspaces.ErrNotConfigured
		}
		if err != nil {
			return err
		}
		if config.ID != expected {
			return workspaces.ErrConflict
		}
		if tenantCapacity > 0 {
			if err := admitTenant(ctx, q, tenant, tenantCapacity, capacityMiB); err != nil {
				return err
			}
		}
		if err := q.InsertEnvironmentWorkspace(ctx, sqlc.InsertEnvironmentWorkspaceParams{
			ObjectID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, EnvironmentID: pgunit.PathID(environment), ConfigurationID: config.ID, CapacityMib: int64(capacityMiB),
		}); err != nil {
			return err
		}
		result, err = get(ctx, q, e.credentials, tenant, environment)
		return err
	})
	if err != nil {
		return workspaces.Record{}, err
	}
	return result, nil
}

// admitTenant serializes one tenant's admissions and counts every workspace
// whose storage is not yet confirmed released. A retained unbounded workspace
// has no admitted capacity to count, so it blocks further admission.
func admitTenant(ctx context.Context, q *sqlc.Queries, tenant string, limit int64, capacityMiB uint32) error {
	if capacityMiB == 0 {
		return fmt.Errorf("%w: tenant capacity admission requires a bounded workspace", workspacefs.ErrUnsupported)
	}
	id := pgunit.PathID(tenant)
	if err := q.LockWorkspaceTenant(ctx, id); err != nil {
		return err
	}
	usage, err := q.TenantWorkspaceCapacity(ctx, id)
	if err != nil {
		return err
	}
	if usage.Unbounded || usage.CommittedMib+int64(capacityMiB) > limit {
		return workspaces.ErrCapacityExhausted
	}
	return nil
}

func (e *Execution) MarkReady(ctx context.Context, ref workspacefs.Reference, attachment workspacefs.Attachment) error {
	return e.change(ctx, ref, func(ctx context.Context, q *sqlc.Queries, current workspaces.Record, deleted bool) error {
		if deleted || current.State != workspaces.Creating {
			return workspaces.ErrConflict
		}
		if err := workspacefs.ValidateAttachment(ref, current.Configuration.Configuration, attachment); err != nil {
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
		current, err := get(ctx, q, e.credentials, ref.TenantID, ref.EnvironmentID)
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

func get(ctx context.Context, q *sqlc.Queries, c credentials, tenant, environment string) (workspaces.Record, error) {
	row, err := q.GetEnvironmentWorkspace(ctx, sqlc.GetEnvironmentWorkspaceParams{TenantID: pgunit.PathID(tenant), ID: pgunit.PathID(environment)})
	if errors.Is(err, pgx.ErrNoRows) {
		return workspaces.Record{}, workspaces.ErrNotFound
	}
	if err != nil {
		return workspaces.Record{}, err
	}
	return record(c, row.EnvironmentWorkspace, row.WorkspaceFsConfiguration, row.TenantID)
}

func publicConfiguration(row sqlc.WorkspaceFsConfiguration) workspacefs.Configuration {
	return workspacefs.Configuration{ID: uuid.UUID(row.ID.Bytes).String(), Adapter: row.Adapter, Parameters: row.Parameters}
}

func record(c credentials, row sqlc.EnvironmentWorkspace, config sqlc.WorkspaceFsConfiguration, tenant pgtype.UUID) (workspaces.Record, error) {
	configuration, err := c.configuration(config)
	if err != nil {
		return workspaces.Record{}, err
	}
	result := workspaces.Record{
		Reference:     workspacefs.Reference{TenantID: uuid.UUID(tenant.Bytes).String(), EnvironmentID: uuid.UUID(row.EnvironmentID.Bytes).String(), ObjectID: uuid.UUID(row.ObjectID.Bytes).String()},
		Configuration: configuration, CapacityMiB: uint32(row.CapacityMib), State: workspaces.State(row.State),
	}
	if row.Attachment != nil {
		var attachment workspacefs.Attachment
		if err := json.Unmarshal(row.Attachment, &attachment); err != nil {
			return workspaces.Record{}, err
		}
		if err := workspacefs.ValidateAttachment(result.Reference, result.Configuration.Configuration, attachment); err != nil {
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
