package sessionpg

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// installationPurpose is the credential key purpose installation
// authorizations are signed under.
const installationPurpose = "environment-installation"

func (s *Store) AuthenticateEnvironmentExecutor(ctx context.Context, environment, digest string) (string, error) {
	id, err := pgunit.ParseID(environment)
	if err != nil {
		return "", sessions.ErrNotFound
	}
	hash, err := hex.DecodeString(digest)
	if err != nil || len(hash) != sha256.Size {
		return "", sessions.ErrNotFound
	}
	tenant, err := s.units.Queries().AuthenticateEnvironmentExecutor(ctx, sqlc.AuthenticateEnvironmentExecutorParams{EnvironmentID: id, TokenSha256: hex.EncodeToString(hash)})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", sessions.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("authenticate environment executor: %w", err)
	}
	return uuid.UUID(tenant.Bytes).String(), nil
}

func (s *Store) ProjectExecutorCredentialState(ctx context.Context, project identity.Principal, environment string) (sessions.ExecutorCredentialState, error) {
	if err := project.Validate(); err != nil {
		return sessions.ExecutorCredentialState{}, sessions.ErrInvalidInput
	}
	tenant, err := parseID(project.TenantID)
	if err != nil {
		return sessions.ExecutorCredentialState{}, err
	}
	environmentID := pgunit.PathID(environment)
	result := sessions.ExecutorCredentialState{Credentials: []sessions.ExecutorCredential{}}
	err = s.units.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		row, err := q.GetEnvironmentExecutorConnection(ctx, sqlc.GetEnvironmentExecutorConnectionParams{EnvironmentID: environmentID, TenantID: tenant})
		if errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrNotFound
		}
		if err != nil {
			return err
		}
		result.EnvironmentID = uuid.UUID(environmentID.Bytes).String()
		result.Connection.EnvironmentStatus = row.EnvironmentStatus
		result.Connection.DeviceID = optionalID(row.DeviceID)
		if row.ExecutorKeyID.Valid {
			key := optionalID(row.ExecutorKeyID)
			result.Connection.BoundKeyID = &key
		}
		if row.EnrolledAt.Valid {
			at := row.EnrolledAt.Time
			result.Connection.EnrolledAt = &at
		}
		if row.LastSeenAt.Valid {
			at := row.LastSeenAt.Time
			result.Connection.LastSeenAt = &at
		}
		result.Connection.CredentialHash = row.CredentialHash.String
		result.Credentials, err = listExecutorCredentials(ctx, q, tenant, environmentID, project.Subject())
		return err
	})
	if err != nil {
		return sessions.ExecutorCredentialState{}, err
	}
	return result, nil
}

func listExecutorCredentials(ctx context.Context, q *sqlc.Queries, tenant, environment pgtype.UUID, subject identity.Subject) ([]sessions.ExecutorCredential, error) {
	rows, err := q.ListEnvironmentExecutorCredentials(ctx, sqlc.ListEnvironmentExecutorCredentialsParams{
		TenantID: tenant, EnvironmentID: environment, SubjectKind: validText(subject.Kind), SubjectID: validText(subject.ID),
	})
	if err != nil {
		return nil, err
	}
	credentials := make([]sessions.ExecutorCredential, 0, len(rows))
	for _, row := range rows {
		credential := sessions.ExecutorCredential{KeyID: optionalID(row.KeyID), CreatedAt: row.CreatedAt.Time}
		if row.RevokedAt.Valid {
			revoked := row.RevokedAt.Time
			credential.RevokedAt = &revoked
		}
		credentials = append(credentials, credential)
	}
	return credentials, nil
}

// validText is a present text value.
func validText(value string) pgtype.Text { return pgtype.Text{String: value, Valid: true} }

func (s *Store) LoadProjectArchived(ctx context.Context, tenant string) (bool, error) {
	id, err := parseID(tenant)
	if err != nil {
		return false, err
	}
	return loadProjectArchived(ctx, s.units.Queries(), id)
}

// loadProjectArchived share-locks the tenant's Project, until the end of the
// transaction q is bound to, and reports whether it is archived.
func loadProjectArchived(ctx context.Context, q *sqlc.Queries, tenant pgtype.UUID) (bool, error) {
	row, err := q.LockProjectByTenant(ctx, tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, sessions.ErrNotFound
	}
	if err != nil {
		return false, err
	}
	return row.ArchivedAt.Valid, nil
}

func (s *Store) ExecutorProjectExists(ctx context.Context, scope identity.ProjectScope) (bool, error) {
	tenant, err := parseID(scope.TenantID)
	if err != nil {
		return false, err
	}
	return s.units.Queries().ExecutorProjectScopeExists(ctx, sqlc.ExecutorProjectScopeExistsParams{TenantID: tenant, OrganizationID: scope.OrganizationID, ProjectID: scope.ProjectID})
}

func (s *Store) LoadExecutorCredentialRestriction(ctx context.Context, principal identity.Principal, key string) (string, error) {
	tenant, err := parseID(principal.TenantID)
	if err != nil {
		return "", err
	}
	id, err := parseID(key)
	if err != nil {
		return "", err
	}
	restriction, err := s.units.Queries().GetExecutorCredentialForPrincipal(ctx, sqlc.GetExecutorCredentialForPrincipalParams{
		KeyID: id, TenantID: tenant, SubjectKind: validText(principal.SubjectKind), SubjectID: validText(principal.SubjectID),
		OrganizationID: principal.OrganizationID, ProjectID: principal.ProjectID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", sessions.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return optionalID(restriction), nil
}

func (s *Store) SignInstallation(_ context.Context, payload string) (string, error) {
	return s.installationSignature(payload)
}

func (s *Store) VerifyInstallation(_ context.Context, payload, signature string) error {
	want, err := s.installationSignature(payload)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(want), []byte(signature)) {
		return sessions.ErrInstallationAuthorization
	}
	return nil
}

// installationSignature is the keyed digest of an installation authorization
// payload. A service without the credential key cannot sign or verify one.
func (s *Store) installationSignature(payload string) (string, error) {
	if s.cipher == nil {
		return "", credentialcrypto.ErrUnavailable
	}
	return s.cipher.Fingerprint(installationPurpose, payload)
}

func (s *Store) WithExecutorCredentials(ctx context.Context, tenant string, apply func(context.Context, sessions.ExecutorCredentialTx) error) error {
	tenantID, err := parseID(tenant)
	if err != nil {
		return err
	}
	return s.units.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return apply(ctx, &executorCredentialTx{q: sqlc.New(tx), tenant: tenantID})
	})
}

// WithEnvironmentExecutorCredentials reads the Environment on the pool before
// it knows which Session to lock.
func (s *Store) WithEnvironmentExecutorCredentials(ctx context.Context, tenant, environment string, apply func(context.Context, sessions.EnvironmentExecutorCredentialTx, sessions.LockedSession) error) error {
	tenantID, err := parseID(tenant)
	if err != nil {
		return err
	}
	if _, err := parseID(environment); err != nil {
		return err
	}
	current, err := LoadEnvironment(ctx, s.units.Queries(), tenant, environment)
	if err != nil {
		return err
	}
	session := pgunit.PathID(current.SessionID)
	return WithSession(ctx, s.units, tenantID, session, func(ctx context.Context, q *sqlc.Queries, locked sessions.LockedSession) error {
		return apply(ctx, &environmentCredentialTx{executorCredentialTx: executorCredentialTx{q: q, tenant: tenantID}, session: session}, locked)
	})
}

// executorCredentialTx is one tenant's executor credentials inside its
// caller's transaction.
type executorCredentialTx struct {
	q      *sqlc.Queries
	tenant pgtype.UUID
}

var _ sessions.ExecutorCredentialTx = (*executorCredentialTx)(nil)

func (t *executorCredentialTx) LockProject(ctx context.Context) (bool, error) {
	return loadProjectArchived(ctx, t.q, t.tenant)
}

func (t *executorCredentialTx) ListExecutorCredentials(ctx context.Context, environment string, subject identity.Subject) ([]sessions.ExecutorCredential, error) {
	id, err := parseID(environment)
	if err != nil {
		return nil, err
	}
	return listExecutorCredentials(ctx, t.q, t.tenant, id, subject)
}

func (t *executorCredentialTx) AuthenticateExecutor(ctx context.Context, environment, digest string) (bool, error) {
	id, err := parseID(environment)
	if err != nil {
		return false, err
	}
	_, err = t.q.AuthenticateEnvironmentExecutor(ctx, sqlc.AuthenticateEnvironmentExecutorParams{EnvironmentID: id, TokenSha256: digest})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (t *executorCredentialTx) IssueExecutorCredential(ctx context.Context, grant sessions.ExecutorCredentialGrant) (sessions.IssuedExecutorCredential, error) {
	key, err := parseID(grant.KeyID)
	if err != nil {
		return sessions.IssuedExecutorCredential{}, err
	}
	var restriction pgtype.UUID
	if grant.EnvironmentID != "" {
		if restriction, err = parseID(grant.EnvironmentID); err != nil {
			return sessions.IssuedExecutorCredential{}, err
		}
	}
	row, err := t.q.IssueExecutorCredential(ctx, sqlc.IssueExecutorCredentialParams{
		KeyID: key, TenantID: t.tenant, SubjectKind: validText(grant.Principal.SubjectKind), SubjectID: validText(grant.Principal.SubjectID),
		OrganizationID: grant.Principal.OrganizationID, ProjectID: grant.Principal.ProjectID, EnvironmentID: restriction, TokenSha256: grant.Digest,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.IssuedExecutorCredential{}, sessions.ErrExecutorCredentialExists
	}
	if err != nil {
		return sessions.IssuedExecutorCredential{}, err
	}
	return sessions.IssuedExecutorCredential{KeyID: optionalID(row.KeyID), EnvironmentID: optionalID(row.EnvironmentID)}, nil
}

func (t *executorCredentialTx) RotateExecutorCredential(ctx context.Context, subject identity.Subject, key, digest string) (sessions.IssuedExecutorCredential, error) {
	id, err := parseID(key)
	if err != nil {
		return sessions.IssuedExecutorCredential{}, err
	}
	row, err := t.q.RotateExecutorCredential(ctx, sqlc.RotateExecutorCredentialParams{KeyID: id, TenantID: t.tenant, SubjectKind: validText(subject.Kind), SubjectID: validText(subject.ID), TokenSha256: digest})
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.IssuedExecutorCredential{}, sessions.ErrNotFound
	}
	if err != nil {
		return sessions.IssuedExecutorCredential{}, err
	}
	return sessions.IssuedExecutorCredential{KeyID: optionalID(row.KeyID), EnvironmentID: optionalID(row.EnvironmentID)}, nil
}

func (t *executorCredentialTx) RevokeExecutorCredential(ctx context.Context, subject identity.Subject, key string) error {
	id, err := parseID(key)
	if err != nil {
		return err
	}
	n, err := t.q.RevokeExecutorCredential(ctx, sqlc.RevokeExecutorCredentialParams{KeyID: id, TenantID: t.tenant, SubjectKind: validText(subject.Kind), SubjectID: validText(subject.ID)})
	if err == nil && n == 0 {
		return sessions.ErrNotFound
	}
	return err
}

func (t *executorCredentialTx) RecordExecutorCredentialAudit(ctx context.Context, action, key string) error {
	return auditpg.RecordAdminMutation(ctx, t.q, optionalID(t.tenant), action, "executor_credential", key)
}

// environmentCredentialTx is the executor credentials of the Session's
// tenant inside the Session's transaction.
type environmentCredentialTx struct {
	executorCredentialTx
	session pgtype.UUID
}

var _ sessions.EnvironmentExecutorCredentialTx = (*environmentCredentialTx)(nil)

func (t *environmentCredentialTx) LoadSessionCreator(ctx context.Context) (identity.Subject, bool, error) {
	row, err := t.q.GetSession(ctx, sqlc.GetSessionParams{TenantID: t.tenant, ID: t.session})
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Subject{}, false, sessions.ErrNotFound
	}
	if err != nil {
		return identity.Subject{}, false, err
	}
	if !row.CreatorKind.Valid || !row.CreatorID.Valid {
		return identity.Subject{}, false, nil
	}
	return identity.Subject{Kind: row.CreatorKind.String, ID: row.CreatorID.String}, true, nil
}
