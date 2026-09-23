package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrRuntimeLegacyOwnership = errors.New("legacy sandbox ownership requires verification on the original backend")

// RuntimeOwnershipVerifier is a startup-only, read-only check of retained resources.
// It must require positive allocation ownership evidence, never absence alone.
type RuntimeOwnershipVerifier func(context.Context, RuntimeAllocation) error

// RuntimeOwnershipFailure carries only a sanitized startup diagnostic.
type RuntimeOwnershipFailure string

func (e RuntimeOwnershipFailure) Error() string { return string(e) }
func runtimeOwnershipReason(err error) string {
	var reason RuntimeOwnershipFailure
	if errors.As(err, &reason) {
		switch reason {
		case "resource_missing", "ownership_mismatch", "resource_unconfirmed", "snapshot_unconfirmed", "provider_unavailable":
			return string(reason)
		}
	}
	return "provider_unavailable"
}

type runtimeAdoptionPlan struct {
	deployment sqlc.RuntimeDeployment
	digest     [32]byte
}

func (s *Store) verifyLegacyRuntimeAdoption(ctx context.Context, selected *RuntimeDeployment, verify RuntimeOwnershipVerifier) (*runtimeAdoptionPlan, error) {
	if selected == nil || selected.ProviderKind == "" {
		return nil, nil
	}
	if err := s.CheckExecutionOwnership(ctx); err != nil {
		return nil, err
	}
	read, cancel := context.WithTimeout(ctx, executionTransactionTimeout)
	previous, err := s.queries.GetRuntimeDeployment(read)
	cancel()
	if err != nil {
		return nil, err
	}
	if previous.ProviderKind != "" {
		return nil, nil
	}
	// Configuration identity is a prerequisite, not evidence of a physical backend.
	if runtimeUUID(previous.InstallationID) != selected.InstallationID || previous.BackendFingerprint != selected.BackendFingerprint {
		return nil, nil
	}
	digest, _, err := legacyRuntimeDigest(ctx, s.queries, func(row sqlc.ListLegacyRuntimeAllocationsRow) error {
		if selected.LocalNodeID == "" || verify == nil || runtimeUUID(row.RuntimeAllocation.ProviderKey) != selected.InstallationID {
			return ErrRuntimeLegacyOwnership
		}
		if err := s.CheckExecutionOwnership(ctx); err != nil {
			return err
		}
		allocation := runtimeAllocationFromRow(row.RuntimeAllocation, row.SessionID, row.TenantID, row.DeletedAt, false)
		check, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := verify(check, allocation)
		cancel()
		if ownerErr := s.CheckExecutionOwnership(ctx); ownerErr != nil {
			return ownerErr
		}
		if err != nil {
			return fmt.Errorf("%w: allocation %s (%s); restore the original backend or resolve its resources with the previous Core before upgrading", ErrRuntimeLegacyOwnership, allocation.ID, runtimeOwnershipReason(err))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &runtimeAdoptionPlan{deployment: previous, digest: digest}, nil
}

// Read in bounded pages; no external resource call holds a database transaction.
func legacyRuntimeDigest(ctx context.Context, q *sqlc.Queries, visit func(sqlc.ListLegacyRuntimeAllocationsRow) error) ([32]byte, int, error) {
	h := sha256.New()
	count := 0
	after := pgtype.UUID{Valid: true}
	for {
		read, cancel := context.WithTimeout(ctx, executionTransactionTimeout)
		rows, err := q.ListLegacyRuntimeAllocations(read, after)
		cancel()
		if err != nil {
			return [32]byte{}, 0, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			raw, err := json.Marshal(row)
			if err != nil {
				return [32]byte{}, 0, err
			}
			_, _ = h.Write(raw)
			count++
			after = row.RuntimeAllocation.ID
			if visit != nil {
				if err := visit(row); err != nil {
					return [32]byte{}, 0, err
				}
			}
		}
	}
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest, count, nil
}

func checkLegacyRuntimeAdoption(ctx context.Context, q *sqlc.Queries, previous sqlc.RuntimeDeployment, plan *runtimeAdoptionPlan) error {
	digest, count, err := legacyRuntimeDigest(ctx, q, nil)
	if err != nil {
		return err
	}
	if plan == nil {
		if count != 0 {
			return ErrRuntimeLegacyOwnership
		}
		return nil
	}
	if !reflect.DeepEqual(previous, plan.deployment) || digest != plan.digest {
		return fmt.Errorf("%w: retained receipts changed during verification; restart Core to verify again", ErrRuntimeLegacyOwnership)
	}
	return nil
}
