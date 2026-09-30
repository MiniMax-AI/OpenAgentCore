package sessionpg

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func (s *Store) GetSessionDevice(ctx context.Context, tenant, session string) (sessions.ExecutionDevice, error) {
	lookup, err := DeviceLookup(tenant, session)
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	ready, err := s.units.Queries().GetSessionInitializationReady(ctx, sqlc.GetSessionInitializationReadyParams(lookup))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !ready) {
		return sessions.ExecutionDevice{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	return s.GetSessionRuntimeDevice(ctx, tenant, session)
}

func (s *Store) GetSessionRuntimeDevice(ctx context.Context, tenant, session string) (sessions.ExecutionDevice, error) {
	lookup, err := DeviceLookup(tenant, session)
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	device, found, err := loadSessionDevice(ctx, s.units.Queries(), lookup.TenantID, lookup.ID)
	if err == nil && !found {
		return sessions.ExecutionDevice{}, sessions.ErrNotFound
	}
	return device, err
}

// loadSessionDevice reads on q the device bound to the tenant's Session and
// reports whether the Session has one that was not revoked.
func loadSessionDevice(ctx context.Context, q *sqlc.Queries, tenant, session pgtype.UUID) (sessions.ExecutionDevice, bool, error) {
	row, err := q.GetSessionDevice(ctx, sqlc.GetSessionDeviceParams{TenantID: tenant, ID: session})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.ExecutionDevice{}, false, nil
	}
	if err != nil {
		return sessions.ExecutionDevice{}, false, err
	}
	return sessions.ExecutionDevice{ID: uuid.UUID(row.ID.Bytes).String(), Name: row.Name, EnvironmentID: optionalID(row.EnvironmentID)}, true, nil
}

// GetDeviceCredential reads a device's credential for the Runtime gateway. A
// malformed or unknown device has no credential. The standalone service
// assigns no product WorkspaceID.
func (s *Store) GetDeviceCredential(ctx context.Context, device string) (runtimedevice.Credential, bool, error) {
	id, err := pgunit.ParseID(device)
	if err != nil {
		return runtimedevice.Credential{}, false, nil
	}
	row, err := s.units.Queries().GetDeviceCredential(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return runtimedevice.Credential{}, false, nil
	}
	if err != nil {
		return runtimedevice.Credential{}, false, err
	}
	return runtimedevice.Credential{
		ID: uuid.UUID(row.ID.Bytes).String(), Name: row.Name, Type: runtimedevice.RuntimeTypeAgentDaemon,
		CredentialHash: row.CredentialHash, RuntimeNodeID: row.RuntimeNodeID, RuntimeAllocationID: row.RuntimeAllocationID,
	}, true, nil
}

func (s *Store) ListEnrolledRuntimeBindings(ctx context.Context) ([]sessions.EnrolledRuntimeBinding, error) {
	rows, err := s.units.Queries().ListEnrolledRuntimeBindings(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]sessions.EnrolledRuntimeBinding, 0, len(rows))
	for _, row := range rows {
		result = append(result, sessions.EnrolledRuntimeBinding{
			DeviceID: optionalID(row.DeviceID), TenantID: optionalID(row.TenantID),
			EnvironmentID: optionalID(row.EnvironmentID), SessionID: optionalID(row.SessionID),
		})
	}
	return result, nil
}

func (s *Store) CreateDevice(ctx context.Context, tenant string, registration sessions.DeviceRegistration) (sessions.ExecutionDevice, error) {
	tenantID, err := parseID(tenant)
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	id, err := s.units.Queries().CreateDevice(ctx, sqlc.CreateDeviceParams{
		ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: tenantID,
		Name: registration.Name, CredentialHash: pgtype.Text{String: registration.CredentialHash, Valid: true},
	})
	if err != nil {
		return sessions.ExecutionDevice{}, fmt.Errorf("create execution device: %w", err)
	}
	return sessions.ExecutionDevice{ID: uuid.UUID(id.Bytes).String(), Name: registration.Name}, nil
}

func (s *Store) RevokeDevice(ctx context.Context, tenant, device string) error {
	lookup, err := DeviceLookup(tenant, device)
	if err != nil {
		return err
	}
	n, err := s.units.Queries().RevokeDevice(ctx, sqlc.RevokeDeviceParams(lookup))
	if err == nil && n == 0 {
		return sessions.ErrNotFound
	}
	return err
}

func (s *Store) TouchDevice(ctx context.Context, device string) (bool, error) {
	id, err := parseID(device)
	if err != nil {
		return false, err
	}
	n, err := s.units.Queries().TouchDevice(ctx, id)
	return n > 0, err
}

func (s *Store) TouchAuthenticatedDevice(ctx context.Context, device, credentialHash string) (bool, error) {
	id, err := parseID(device)
	if err != nil {
		return false, err
	}
	n, err := s.units.Queries().TouchAuthenticatedDevice(ctx, sqlc.TouchAuthenticatedDeviceParams{ID: id, CredentialHash: credentialHash})
	return n > 0, err
}

// WithEnrollment authenticates the credential and reads the Environment on
// the pool before it knows which Session to lock; the enrollment transaction
// rechecks that authority under the Session lock.
func (s *Store) WithEnrollment(ctx context.Context, environment, credentialHash string, apply func(context.Context, sessions.EnrollmentTx, sessions.Environment, sessions.LockedSession) error) error {
	tenant, err := s.AuthenticateEnvironmentExecutor(ctx, environment, credentialHash)
	if err != nil {
		return err
	}
	current, err := s.GetEnvironment(ctx, tenant, environment)
	if err != nil {
		return err
	}
	lookup, err := DeviceLookup(tenant, current.ID)
	if err != nil {
		return err
	}
	return WithSession(ctx, s.units, lookup.TenantID, pgunit.PathID(current.SessionID), func(ctx context.Context, q *sqlc.Queries, locked sessions.LockedSession) error {
		tx := &enrollmentTx{SessionTx: BindSession(q, lookup.TenantID, pgunit.PathID(current.SessionID)), environment: lookup.ID, credentialHash: credentialHash}
		return apply(ctx, tx, current, locked)
	})
}

// enrollmentTx is one enrollment inside the transaction of the Environment's
// Session.
type enrollmentTx struct {
	*SessionTx
	environment    pgtype.UUID
	credentialHash string
}

var _ sessions.EnrollmentTx = (*enrollmentTx)(nil)

func (t *enrollmentTx) AuthorizeEnrollment(ctx context.Context) (sessions.EnrollmentAuthority, error) {
	row, err := t.q.AuthorizeRuntimeEnrollment(ctx, sqlc.AuthorizeRuntimeEnrollmentParams{EnvironmentID: t.environment, TenantID: t.tenant, TokenSha256: t.credentialHash})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.EnrollmentAuthority{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.EnrollmentAuthority{}, err
	}
	return sessions.EnrollmentAuthority{KeyID: optionalID(row.KeyID), WorkspaceDirectory: row.WorkspaceDirectory}, nil
}

func (t *enrollmentTx) EnrollDevice(ctx context.Context, key string) (string, error) {
	keyID, err := parseID(key)
	if err != nil {
		return "", err
	}
	row, err := t.q.EnrollRuntimeDevice(ctx, sqlc.EnrollRuntimeDeviceParams{
		ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: t.tenant, EnvironmentID: t.environment, ExecutorKeyID: keyID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", sessions.ErrDeviceBindingConflict
	}
	if err != nil {
		return "", err
	}
	return uuid.UUID(row.ID.Bytes).String(), nil
}

var _ sessions.DeviceBindingTx = (*SessionTx)(nil)

func (t *SessionTx) LoadDevice(ctx context.Context, device string) (bool, error) {
	id, err := parseID(device)
	if err != nil {
		return false, err
	}
	_, err = t.q.GetDevice(ctx, sqlc.GetDeviceParams{TenantID: t.tenant, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// BindDevice binds the device to the Session; a Session bound to another
// device is sessions.ErrDeviceBindingConflict.
func (t *SessionTx) BindDevice(ctx context.Context, device string) error {
	id, err := parseID(device)
	if err != nil {
		return err
	}
	_, err = t.q.BindSessionDevice(ctx, sqlc.BindSessionDeviceParams{TenantID: t.tenant, ID: t.session, ID_2: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.ErrDeviceBindingConflict
	}
	return err
}
