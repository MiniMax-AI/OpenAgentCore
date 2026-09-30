package store

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ArchiveManagedSession ends a managed Environment's lifetime while keeping its
// public Session, history and persisted files. The lifecycle owner performs the
// external cleanup; only its existing confirmation can release an allocation.
func (s *Store) ArchiveManagedSession(ctx context.Context, tenantID, sessionID string, expectedGeneration uint64) (sessions.ManagedArchive, error) {
	return s.archiveManagedSession(ctx, tenantID, sessionID, expectedGeneration, nil)
}

// A reset instance is identified by its persisted request time as well as its
// generation, preventing a cancelled clear's candidates from affecting its successor.
func (s *Store) ArchiveSandboxResetSession(ctx context.Context, tenantID, sessionID string, generation uint64, requestedAt time.Time) (sessions.ManagedArchive, error) {
	return s.archiveManagedSession(ctx, tenantID, sessionID, generation, &requestedAt)
}

var ErrSandboxResetSessionBusy = errors.New("the hosted Session is busy")

func (s *Store) archiveManagedSession(ctx context.Context, tenantID, sessionID string, expectedGeneration uint64, resetRequestedAt *time.Time) (sessions.ManagedArchive, error) {
	if err := s.checkExecutionAuthority(); err != nil {
		return sessions.ManagedArchive{}, err
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.ManagedArchive{}, err
	}
	var result sessions.ManagedArchive
	err = s.withPublicSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		// Session precedes deployment, matching Turn, allocation and input admission.
		current, err := q.LockRuntimeDeployment(ctx)
		if err != nil {
			return err
		}
		if uint64(current.Generation) != expectedGeneration {
			return &deployment.GenerationStaleError{CurrentGeneration: uint64(current.Generation)}
		}
		if !current.WebManaged || !current.InstallationID.Valid {
			return deployment.ErrConflict
		}
		if current.ProviderKind == "" {
			return deployment.ErrNotConfigured
		}
		environment, err := q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: tenant, ID: session})
		if errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrInvalidInput
		}
		if err != nil {
			return err
		}
		kind, err := sessions.EnvironmentType(environment.Configuration)
		if err != nil || kind != "openai_hosted" {
			return sessions.ErrInvalidInput
		}
		if resetRequestedAt != nil {
			if !current.ResetClear.Valid || !current.ResetRequestedAt.Time.Equal(*resetRequestedAt) {
				return deployment.ErrConflict
			}
			if current.ResetClear.String == "auto" {
				busy, err := q.SessionBlocksAutoReset(ctx, session)
				if err != nil {
					return err
				}
				if busy {
					return ErrSandboxResetSessionBusy
				}
			}
			if environment.Environment.Status == "failed" || environment.Environment.Status == "expired" {
				result, err = getManagedSessionArchive(ctx, q, tenant, session)
				return err
			}
			source, err := sandboxResetAudit(current)
			if err != nil {
				return err
			}
			project, err := q.GetSandboxResetProject(ctx, tenant)
			if err != nil {
				return err
			}
			source.ProjectID = runtimeUUID(project)
			ctx = adminaudit.WithSource(ctx, source)
		}
		allocation, err := q.GetRuntimeAllocation(ctx, sqlc.GetRuntimeAllocationParams{TenantID: tenant, EnvironmentID: environment.Environment.ID})
		allocated := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if allocated && allocation.RuntimeAllocation.State != "released" && allocation.RuntimeAllocation.ProviderKey != current.InstallationID {
			return deployment.ErrConflict
		}
		bound := sessionpg.BindSession(q, tenant, session)
		if err := sessions.TrackInputActivity(ctx, bound, func(ctx context.Context) error {
			if environment.Environment.Status != "failed" && environment.Environment.Status != "expired" {
				if err := q.SetEnvironmentConnectionStatus(ctx, sqlc.SetEnvironmentConnectionStatusParams{ID: environment.Environment.ID, Status: "expired"}); err != nil {
					return err
				}
			}
			return sessions.CancelWork(ctx, bound)
		}); err != nil {
			return err
		}
		if allocated && allocation.RuntimeAllocation.State != "released" {
			if _, err := q.RevokeArchivedRuntimeDevice(ctx, sqlc.RevokeArchivedRuntimeDeviceParams{TenantID: tenant, DeviceID: allocation.RuntimeAllocation.DeviceID, SessionID: session}); err != nil {
				return err
			}
			if _, err := q.RequestRuntimeCleanup(ctx, allocation.RuntimeAllocation.ID); err != nil {
				return err
			}
		} else if !allocated {
			if err := q.ReleaseUnallocatedRuntimePlacement(ctx, session); err != nil {
				return err
			}
		}
		if err := auditpg.RecordAdminMutation(ctx, q, tenantID, "archive", "session", runtimeUUID(session)); err != nil {
			return err
		}
		result, err = getManagedSessionArchive(ctx, q, tenant, session)
		return err
	})
	return result, err
}

// GetManagedSessionArchive reads one database snapshot and never contacts compute.
func (s *Store) GetManagedSessionArchive(ctx context.Context, tenantID, sessionID string) (sessions.ManagedArchive, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return sessions.ManagedArchive{}, err
	}
	return getManagedSessionArchive(ctx, s.queries, tenant, pgunit.PathID(sessionID))
}

func getManagedSessionArchive(ctx context.Context, q *sqlc.Queries, tenant, session pgtype.UUID) (sessions.ManagedArchive, error) {
	row, err := q.GetManagedSessionArchive(ctx, sqlc.GetManagedSessionArchiveParams{TenantID: tenant, ID: session})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.ManagedArchive{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.ManagedArchive{}, err
	}
	if row.EnvironmentType != "openai_hosted" {
		return sessions.ManagedArchive{}, sessions.ErrInvalidInput
	}
	return sessions.ManagedArchive{SessionID: runtimeUUID(row.SessionID), EnvironmentID: runtimeUUID(row.EnvironmentID), State: row.State}, nil
}
