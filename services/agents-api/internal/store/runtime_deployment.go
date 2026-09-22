package store

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

// RuntimeDeployment identifies the one operator-selected installation for this database.
// Its fingerprint describes the backend namespace, never credentials or image contents.
type RuntimeDeployment struct {
	InstallationID     string
	BackendFingerprint string
	Maintenance        bool
}

// ConfigureRuntimeDeployment runs before Worker startup under its execution lease.
// Maintenance must be committed for the old installation before any switch.
// A nil selection never forgets the previous identity or unresolved resources.
func (s *Store) ConfigureRuntimeDeployment(ctx context.Context, selected *RuntimeDeployment) error {
	if s.executionLease == nil {
		return ErrInvalidInput
	}
	var update sqlc.SetRuntimeDeploymentParams
	if selected != nil {
		id, err := parseConnectionGeneration(selected.InstallationID)
		if err != nil {
			return err
		}
		digest, err := hex.DecodeString(selected.BackendFingerprint)
		if err != nil || len(digest) != 32 || strings.ToLower(selected.BackendFingerprint) != selected.BackendFingerprint {
			return fmt.Errorf("%w: invalid backend identity fingerprint", ErrInvalidInput)
		}
		update = sqlc.SetRuntimeDeploymentParams{InstallationID: id, BackendFingerprint: selected.BackendFingerprint, Maintenance: selected.Maintenance}
	}
	return s.executionLease.transaction(ctx, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		previous, err := q.LockRuntimeDeployment(ctx)
		if err != nil {
			return err
		}
		if selected != nil && previous.InstallationID == update.InstallationID && previous.BackendFingerprint == update.BackendFingerprint {
			return q.SetRuntimeDeployment(ctx, update)
		}
		resources, err := q.CountRuntimeDeploymentResources(ctx)
		if err != nil {
			return err
		}
		if selected == nil {
			if previous.InstallationID.Valid && (resources.Allocations != 0 || resources.Pending != 0) {
				return fmt.Errorf("cannot disable managed sandbox provider: %d unreleased allocations (instances, retained snapshots, uncertain operations or pending cleanup) and %d pending hosted environments remain", resources.Allocations, resources.Pending)
			}
			return nil
		}
		if !previous.InstallationID.Valid {
			if resources.Allocations != 0 {
				return fmt.Errorf("cannot adopt sandbox installation: %d existing unreleased allocations (including retained snapshots and pending cleanup) have no verified backend identity", resources.Allocations)
			}
		} else {
			if !previous.Maintenance || !selected.Maintenance {
				return fmt.Errorf("cannot switch sandbox installation: persist maintenance on the previous installation and keep the new installation in maintenance")
			}
			if resources.Allocations != 0 || resources.Pending != 0 {
				return fmt.Errorf("cannot switch sandbox installation: %d unreleased allocations (instances, retained snapshots, uncertain operations or pending cleanup) and %d pending hosted environments remain", resources.Allocations, resources.Pending)
			}
		}
		return q.SetRuntimeDeployment(ctx, update)
	})
}

// New work and deployment changes share this lock. Existing receipts are checked
// first, preserving idempotent retries and cleanup while maintenance is active.
func checkRuntimeDeploymentAdmission(ctx context.Context, q *sqlc.Queries, installation string) error {
	current, err := q.LockRuntimeDeployment(ctx)
	if err != nil {
		return err
	}
	if !current.InstallationID.Valid {
		return nil
	}
	if current.Maintenance {
		return fmt.Errorf("%w: sandbox creation is paused for provider maintenance", ErrEnvironmentUnavailable)
	}
	if installation != "" {
		id, err := parseConnectionGeneration(installation)
		if err != nil || id != current.InstallationID {
			return fmt.Errorf("%w: sandbox installation does not match deployment", ErrEnvironmentUnavailable)
		}
	}
	return nil
}
