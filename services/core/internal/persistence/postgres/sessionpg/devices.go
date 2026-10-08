package sessionpg

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func (s *Store) GetSessionDevice(ctx context.Context, tenant, session string) (sessions.ExecutionDevice, error) {
	lookup, err := ResourceLookup(tenant, session)
	if err != nil {
		return sessions.ExecutionDevice{}, err
	}
	if err := requireInitialized(ctx, s.units.Queries(), lookup); err != nil {
		return sessions.ExecutionDevice{}, err
	}
	return s.GetSessionRuntimeDevice(ctx, tenant, session)
}

func (s *Store) GetSessionExecutionBinding(ctx context.Context, tenant, session string) (sessions.ExecutionBinding, error) {
	lookup, err := ResourceLookup(tenant, session)
	if err != nil {
		return sessions.ExecutionBinding{}, err
	}
	q := s.units.Queries()
	if err := requireInitialized(ctx, q, lookup); err != nil {
		return sessions.ExecutionBinding{}, err
	}
	row, err := q.GetSessionExecutionBinding(ctx, sqlc.GetSessionExecutionBindingParams(lookup))
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.ExecutionBinding{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.ExecutionBinding{}, err
	}
	return sessions.ExecutionBinding{
		Device: sessions.ExecutionDevice{ID: uuid.UUID(row.ID.Bytes).String(), Name: row.Name,
			Assignment: assignmentRef(lookup.ID, row.AssignmentID, row.Epoch), SessionEnvironmentID: optionalID(row.SessionEnvironmentID)},
		NativeSessionID: row.NativeSessionID,
		HasStartedTurn:  row.HasStartedTurn,
	}, nil
}

// requireInitialized requires that the tenant's Session completed its
// Environment preparation; before that, and for a missing Session, it is
// sessions.ErrNotFound.
func requireInitialized(ctx context.Context, q *sqlc.Queries, lookup Lookup) error {
	ready, err := q.GetSessionInitializationReady(ctx, sqlc.GetSessionInitializationReadyParams(lookup))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !ready) {
		return sessions.ErrNotFound
	}
	return err
}

func (s *Store) ListAgentHosts(ctx context.Context, tenant string) ([]sessions.ExecutionDevice, error) {
	_, err := parseID(tenant)
	if err != nil {
		return nil, err
	}
	rows, err := s.units.Queries().ListAgentHosts(ctx)
	if err != nil {
		return nil, err
	}
	devices := make([]sessions.ExecutionDevice, 0, len(rows))
	for _, row := range rows {
		devices = append(devices, sessions.ExecutionDevice{ID: uuid.UUID(row.ID.Bytes).String(), Name: row.Name})
	}
	return devices, nil
}

func (s *Store) GetSessionRuntimeDevice(ctx context.Context, tenant, session string) (sessions.ExecutionDevice, error) {
	lookup, err := ResourceLookup(tenant, session)
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
	return sessions.ExecutionDevice{ID: uuid.UUID(row.ID.Bytes).String(), Name: row.Name,
		Assignment: assignmentRef(session, row.AssignmentID, row.Epoch), SessionEnvironmentID: optionalID(row.SessionEnvironmentID)}, true, nil
}

// assignmentRef is the reference of a Session's assignment; an absent
// assignment is the zero reference.
func assignmentRef(session, assignment pgtype.UUID, epoch int64) proto.AssignmentRef {
	if !assignment.Valid {
		return proto.AssignmentRef{}
	}
	return proto.AssignmentRef{SessionID: optionalID(session), AssignmentID: optionalID(assignment), Epoch: uint64(epoch)}
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
		CredentialHash: row.CredentialHash,
	}, true, nil
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
	lookup, err := ResourceLookup(tenant, current.ID)
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

func (t *enrollmentTx) AuthorizeEnrollment(ctx context.Context) (string, error) {
	key, err := t.q.AuthorizeRuntimeEnrollment(ctx, sqlc.AuthorizeRuntimeEnrollmentParams{EnvironmentID: t.environment, TenantID: t.tenant, TokenSha256: t.credentialHash})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", sessions.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return optionalID(key), nil
}

func (t *enrollmentTx) EnrollSandbox(ctx context.Context, key string) (sandboxbootstrap.Resource, error) {
	keyID, err := parseID(key)
	if err != nil {
		return sandboxbootstrap.Resource{}, err
	}
	row, err := t.q.EnrollSandbox(ctx, sqlc.EnrollSandboxParams{
		ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, EnvironmentID: t.environment, ExecutorKeyID: keyID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sandboxbootstrap.Resource{}, sessions.ErrDeviceBindingConflict
	}
	if err != nil {
		return sandboxbootstrap.Resource{}, err
	}
	return linkResource(t.tenant, t.environment, pgtype.Text{String: "enrollment", Valid: true}, row.ID, pgtype.Int8{Int64: row.Generation, Valid: true}), nil
}

var _ sessions.DeviceBindingTx = (*SessionTx)(nil)

func (t *SessionTx) LoadDevice(ctx context.Context, device string) (bool, error) {
	id, err := parseID(device)
	if err != nil {
		return false, err
	}
	_, err = t.q.GetAgentHost(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// ReleaseAssignment records that the Session's Runtime assignment is
// released, with home removal if removeHome, and advances its epoch unless
// that release is already recorded. A Session without an assignment has
// nothing to release.
func (t *SessionTx) ReleaseAssignment(ctx context.Context, removeHome bool) error {
	if err := t.q.LockAssignmentRuntime(ctx, t.session); err != nil {
		return err
	}
	return t.q.ReleaseSessionAssignment(ctx, sqlc.ReleaseSessionAssignmentParams{SessionID: t.session, RemoveHome: removeHome})
}

// BindDevice binds the agent host to the Session; a Session bound to another
// Runtime, a released assignment, or a Session whose Environment has no live
// Link resource is sessions.ErrDeviceBindingConflict.
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
