package deployment

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// ArchiveSession ends the lifetime of a hosted Session's Environment while
// keeping the public Session, its history and persisted files. It cancels the
// Session's work, revokes the allocation's device and requests its cleanup;
// the lifecycle performs the provider work, and only its confirmation releases
// the allocation. expectedGeneration must be the current generation of the Web
// managed deployment.
func (e *ExecutionOperations) ArchiveSession(ctx context.Context, tenantID, sessionID string, expectedGeneration uint64) (sessions.ManagedArchive, error) {
	return e.archiveSession(ctx, tenantID, sessionID, expectedGeneration, nil)
}

// ArchiveResetSession archives a hosted Session for the running reset, which
// its generation and request time identify, so that a cancelled reset's
// candidates never affect its successor. An automatic reset skips a busy
// Session with ErrSandboxResetSessionBusy; an Environment that already ended
// only reports its disposal. The archive is audited with the reset's
// administrator source.
func (e *ExecutionOperations) ArchiveResetSession(ctx context.Context, tenantID, sessionID string, generation uint64, requestedAt time.Time) (sessions.ManagedArchive, error) {
	return e.archiveSession(ctx, tenantID, sessionID, generation, &requestedAt)
}

func (e *ExecutionOperations) archiveSession(ctx context.Context, tenantID, sessionID string, generation uint64, resetRequestedAt *time.Time) (sessions.ManagedArchive, error) {
	var result sessions.ManagedArchive
	err := e.storage.WithSessionArchive(ctx, tenantID, sessionID, func(ctx context.Context, locked sessions.LockedSession, tx SessionArchiveTx) error {
		if err := locked.Public(); err != nil {
			return err
		}
		d, err := tx.LoadDeployment()
		if err != nil {
			return err
		}
		if err := checkArchiveDeployment(d, generation); err != nil {
			return err
		}
		environment, err := tx.LoadEnvironment(ctx)
		if errors.Is(err, sessions.ErrNotFound) {
			return sessions.ErrInvalidInput
		}
		if err != nil {
			return err
		}
		if kind, err := sessions.EnvironmentType(environment.Configuration); err != nil || kind != "openai_hosted" {
			return sessions.ErrInvalidInput
		}
		ended := environment.Status == "failed" || environment.Status == "expired"
		if resetRequestedAt != nil {
			if err := checkArchiveReset(d, *resetRequestedAt); err != nil {
				return err
			}
			if d.Reset.Clear == ResetAuto {
				busy, err := tx.LoadResetBusy()
				if err != nil {
					return err
				}
				if busy {
					return ErrSandboxResetSessionBusy
				}
			}
			if ended {
				result, err = tx.LoadArchive(ctx)
				return err
			}
			source, err := tx.LoadResetSource()
			if err != nil {
				return err
			}
			if source.ProjectID, err = tx.LoadProject(); err != nil {
				return err
			}
			ctx = adminaudit.WithSource(ctx, source)
		}
		current, allocated, err := tx.FindAllocation(environment.ID)
		if err != nil {
			return err
		}
		live := allocated && current.State != "released"
		if live && current.ProviderKey != d.InstallationID {
			return ErrConflict
		}
		if err := sessions.TrackInputActivity(ctx, tx, func(ctx context.Context) error {
			if !ended {
				if err := tx.ExpireEnvironment(ctx, environment.ID); err != nil {
					return err
				}
			}
			return sessions.CancelWork(ctx, tx)
		}); err != nil {
			return err
		}
		if live {
			if err := tx.RequestArchiveCleanup(current); err != nil {
				return err
			}
		} else if !allocated {
			if err := tx.ReleasePlacement(); err != nil {
				return err
			}
		}
		if err := tx.RecordArchiveAudit(ctx); err != nil {
			return err
		}
		result, err = tx.LoadArchive(ctx)
		return err
	})
	if err != nil {
		return sessions.ManagedArchive{}, err
	}
	return result, nil
}

// checkArchiveDeployment rejects an archive against a deployment whose
// generation is not the expected one, that Web does not manage or that has no
// provider.
func checkArchiveDeployment(d Record, expectedGeneration uint64) error {
	if d.Generation != expectedGeneration {
		return &GenerationStaleError{CurrentGeneration: d.Generation}
	}
	if !d.WebManaged || d.InstallationID == "" {
		return ErrConflict
	}
	if d.Provider == "" {
		return ErrNotConfigured
	}
	return nil
}

// checkArchiveReset rejects a reset archive unless the reset requested at
// requestedAt is still running.
func checkArchiveReset(d Record, requestedAt time.Time) error {
	if d.Reset == nil || !d.Reset.RequestedAt.Equal(requestedAt) {
		return ErrConflict
	}
	return nil
}
