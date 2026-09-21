package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// RuntimeEnrollment returns the immutable resource binding, never another secret.
type RuntimeEnrollment struct {
	DeviceID           string
	SessionID          string
	EnvironmentID      string
	WorkspaceDirectory string
}

// EnrollRuntime binds our daemon to one self-hosted Environment. Retries retain
// the same device/key; they cannot replace compute or adopt another native history.
func (s *Store) EnrollRuntime(ctx context.Context, environmentID, credentialHash string) (RuntimeEnrollment, error) {
	tenant, err := s.AuthenticateEnvironmentExecutor(ctx, environmentID, credentialHash)
	if err != nil {
		return RuntimeEnrollment{}, err
	}
	environment, err := s.GetEnvironment(ctx, tenant, environmentID)
	if err != nil {
		return RuntimeEnrollment{}, err
	}
	lookup, err := deviceLookup(tenant, environmentID)
	if err != nil {
		return RuntimeEnrollment{}, err
	}
	var result RuntimeEnrollment
	err = s.withPublicSession(ctx, tenant, environment.SessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		// Recheck authority while holding the Session lock, and retain the key
		// lock until binding commits so revocation cannot race enrollment.
		authority, err := q.AuthorizeRuntimeEnrollment(ctx, sqlc.AuthorizeRuntimeEnrollmentParams{
			EnvironmentID: lookup.ID, TenantID: lookup.TenantID, TokenSha256: credentialHash,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		bound, err := q.EnrollRuntimeDevice(ctx, sqlc.EnrollRuntimeDeviceParams{
			ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: lookup.TenantID,
			EnvironmentID: lookup.ID, ExecutorKeyID: authority.KeyID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDeviceBindingConflict
		}
		if err != nil {
			return err
		}
		_, err = q.BindSessionDevice(ctx, sqlc.BindSessionDeviceParams{TenantID: lookup.TenantID, ID: session, ID_2: bound.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDeviceBindingConflict
		}
		if err != nil {
			return err
		}
		result = RuntimeEnrollment{DeviceID: uuid.UUID(bound.ID.Bytes).String(), SessionID: environment.SessionID,
			EnvironmentID: environment.ID, WorkspaceDirectory: authority.WorkspaceDirectory}
		return nil
	})
	return result, err
}

// EnrolledRuntimeBinding identifies user-managed compute, without an allocation
// or any promise of live authorization. The Worker rechecks the socket's key.
type EnrolledRuntimeBinding struct {
	DeviceID, TenantID, EnvironmentID, SessionID string
}

func (s *Store) ListEnrolledRuntimeBindings(ctx context.Context) ([]EnrolledRuntimeBinding, error) {
	rows, err := s.queries.ListEnrolledRuntimeBindings(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]EnrolledRuntimeBinding, 0, len(rows))
	for _, row := range rows {
		result = append(result, EnrolledRuntimeBinding{uuid.UUID(row.DeviceID.Bytes).String(), uuid.UUID(row.TenantID.Bytes).String(), uuid.UUID(row.EnvironmentID.Bytes).String(), uuid.UUID(row.SessionID.Bytes).String()})
	}
	return result, nil
}
