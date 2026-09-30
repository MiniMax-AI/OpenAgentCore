package store

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ManagedSessionArchive reports resource disposal, not archive request provenance
// or Turn settlement. Existing expiry and failed provisioning use the same states.
type ManagedSessionArchive struct {
	SessionID     string `json:"session_id"`
	EnvironmentID string `json:"environment_id"`
	State         string `json:"state"`
}

// ArchiveManagedSession ends a managed Environment's lifetime while keeping its
// public Session, history and persisted files. The lifecycle owner performs the
// external cleanup; only its existing confirmation can release an allocation.
func (s *Store) ArchiveManagedSession(ctx context.Context, tenantID, sessionID string, expectedGeneration uint64) (ManagedSessionArchive, error) {
	return s.archiveManagedSession(ctx, tenantID, sessionID, expectedGeneration, nil)
}

// A reset instance is identified by its persisted request time as well as its
// generation, preventing a cancelled clear's candidates from affecting its successor.
func (s *Store) ArchiveSandboxResetSession(ctx context.Context, tenantID, sessionID string, generation uint64, requestedAt time.Time) (ManagedSessionArchive, error) {
	return s.archiveManagedSession(ctx, tenantID, sessionID, generation, &requestedAt)
}

var ErrSandboxResetSessionBusy = errors.New("the hosted Session is busy")

func (s *Store) archiveManagedSession(ctx context.Context, tenantID, sessionID string, expectedGeneration uint64, resetRequestedAt *time.Time) (ManagedSessionArchive, error) {
	if err := s.checkExecutionAuthority(); err != nil {
		return ManagedSessionArchive{}, err
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return ManagedSessionArchive{}, err
	}
	var result ManagedSessionArchive
	err = s.withPublicSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		// Session precedes deployment, matching Turn, allocation and input admission.
		deployment, err := q.LockRuntimeDeployment(ctx)
		if err != nil {
			return err
		}
		if uint64(deployment.Generation) != expectedGeneration {
			return &SandboxGenerationStaleError{uint64(deployment.Generation)}
		}
		if !deployment.WebManaged || !deployment.InstallationID.Valid {
			return ErrSandboxDeploymentConflict
		}
		if deployment.ProviderKind == "" {
			return ErrSandboxNotConfigured
		}
		environment, err := q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: tenant, ID: session})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidInput
		}
		if err != nil {
			return err
		}
		kind, err := storedEnvironmentType(environment)
		if err != nil || kind != "openai_hosted" {
			return ErrInvalidInput
		}
		if resetRequestedAt != nil {
			if !deployment.ResetClear.Valid || !deployment.ResetRequestedAt.Time.Equal(*resetRequestedAt) {
				return ErrSandboxDeploymentConflict
			}
			if deployment.ResetClear.String == "auto" {
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
			source, err := sandboxResetAudit(deployment)
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
		if allocated && allocation.RuntimeAllocation.State != "released" && allocation.RuntimeAllocation.ProviderKey != deployment.InstallationID {
			return ErrSandboxDeploymentConflict
		}
		if err := withEnvironmentInputActivity(ctx, q, session, func() error {
			if environment.Environment.Status != "failed" && environment.Environment.Status != "expired" {
				if err := q.SetEnvironmentConnectionStatus(ctx, sqlc.SetEnvironmentConnectionStatusParams{ID: environment.Environment.ID, Status: "expired"}); err != nil {
					return err
				}
			}
			return cancelSessionWork(ctx, q, session)
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
		if err := recordAdminMutation(ctx, q, tenantID, "archive", "session", runtimeUUID(session)); err != nil {
			return err
		}
		result, err = getManagedSessionArchive(ctx, q, tenant, session)
		return err
	})
	return result, err
}

// GetManagedSessionArchive reads one database snapshot and never contacts compute.
func (s *Store) GetManagedSessionArchive(ctx context.Context, tenantID, sessionID string) (ManagedSessionArchive, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return ManagedSessionArchive{}, err
	}
	return getManagedSessionArchive(ctx, s.queries, tenant, parsePathID(sessionID))
}

func getManagedSessionArchive(ctx context.Context, q *sqlc.Queries, tenant, session pgtype.UUID) (ManagedSessionArchive, error) {
	row, err := q.GetManagedSessionArchive(ctx, sqlc.GetManagedSessionArchiveParams{TenantID: tenant, ID: session})
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedSessionArchive{}, ErrNotFound
	}
	if err != nil {
		return ManagedSessionArchive{}, err
	}
	if row.EnvironmentType != "openai_hosted" {
		return ManagedSessionArchive{}, ErrInvalidInput
	}
	return ManagedSessionArchive{SessionID: runtimeUUID(row.SessionID), EnvironmentID: runtimeUUID(row.EnvironmentID), State: row.State}, nil
}
