package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ExecutorCredential is the metadata of one Environment executor credential.
// Its secret is returned only when issued or rotated.
type ExecutorCredential struct {
	KeyID     string     `json:"key_id" format:"uuid"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at" extensions:"x-nullable"`
}

// The Project* methods serve Core-key executor credential management. project
// is the Project's own principal, which becomes the credential's execution
// principal; the target must be a self_hosted Environment of that Project whose
// Session is not deleted, and only credentials restricted to it are managed.

// ExecutorConnectionState is an internal durable observation, never a wire payload.
// In particular the current credential digest must not be serialized.
type ExecutorConnectionState struct {
	DeviceID          string     `json:"-"`
	BoundKeyID        *string    `json:"-"`
	EnrolledAt        *time.Time `json:"-"`
	LastSeenAt        *time.Time `json:"-"`
	CredentialHash    string     `json:"-"`
	EnvironmentStatus string     `json:"-"`
}

type ExecutorCredentialState struct {
	EnvironmentID string `json:"-"`
	Credentials   []ExecutorCredential
	Connection    ExecutorConnectionState
}

func (s *Store) ListProjectExecutorCredentials(ctx context.Context, project identity.Principal, environment string) ([]ExecutorCredential, error) {
	state, err := s.ProjectExecutorCredentialState(ctx, project, environment)
	return state.Credentials, err
}

// ProjectExecutorCredentialState reads list metadata and binding facts in one
// read-only snapshot. The snapshot ends before any live peer/authority observation.
func (s *Store) ProjectExecutorCredentialState(ctx context.Context, project identity.Principal, environment string) (ExecutorCredentialState, error) {
	if err := project.Validate(); err != nil {
		return ExecutorCredentialState{}, ErrInvalidInput
	}
	tenant, err := parseID(project.TenantID)
	if err != nil {
		return ExecutorCredentialState{}, err
	}
	environmentID := parsePathID(environment)
	result := ExecutorCredentialState{Credentials: []ExecutorCredential{}}
	err = s.pooled.Snapshot(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		row, err := q.GetEnvironmentExecutorConnection(ctx, sqlc.GetEnvironmentExecutorConnectionParams{EnvironmentID: environmentID, TenantID: tenant})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		result.EnvironmentID = uuid.UUID(environmentID.Bytes).String()
		result.Connection.EnvironmentStatus = row.EnvironmentStatus
		if row.DeviceID.Valid {
			result.Connection.DeviceID = uuid.UUID(row.DeviceID.Bytes).String()
		}
		if row.ExecutorKeyID.Valid {
			key := uuid.UUID(row.ExecutorKeyID.Bytes).String()
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
		if row.CredentialHash.Valid {
			result.Connection.CredentialHash = row.CredentialHash.String
		}
		rows, err := q.ListEnvironmentExecutorCredentials(ctx, sqlc.ListEnvironmentExecutorCredentialsParams{
			TenantID: tenant, EnvironmentID: environmentID,
			SubjectKind: pgtype.Text{String: project.SubjectKind, Valid: true}, SubjectID: pgtype.Text{String: project.SubjectID, Valid: true},
		})
		if err != nil {
			return err
		}
		for _, row := range rows {
			credential := ExecutorCredential{KeyID: uuid.UUID(row.KeyID.Bytes).String(), CreatedAt: row.CreatedAt.Time}
			if row.RevokedAt.Valid {
				revoked := row.RevokedAt.Time
				credential.RevokedAt = &revoked
			}
			result.Credentials = append(result.Credentials, credential)
		}
		return nil
	})
	return result, err
}

// IssueProjectExecutorCredential issues a new key or, with rotate, replaces the
// secret of an existing key restricted to the Environment. An archived Project
// gets neither (ErrProjectArchived). The administrator audit entry commits in
// the same transaction and never contains the secret.
func (s *Store) IssueProjectExecutorCredential(ctx context.Context, project identity.Principal, environment, keyID string, rotate bool) (IssuedExecutorCredential, error) {
	// The target is checked first, then the archived Project, then the key.
	if err := s.selfHostedExecutorTarget(ctx, project, environment); err != nil {
		return IssuedExecutorCredential{}, err
	}
	if err := activeProject(ctx, s.queries, project); err != nil {
		return IssuedExecutorCredential{}, err
	}
	if !rotate {
		return s.issueExecutorCredential(ctx, project, keyID, environment, activeProjectAudit(project, "issue", keyID))
	}
	if err := s.exactExecutorRestriction(ctx, project, environment, keyID); err != nil {
		return IssuedExecutorCredential{}, err
	}
	return s.rotateExecutorCredential(ctx, project, keyID, activeProjectAudit(project, "rotate", keyID))
}

// RevokeProjectExecutorCredential is idempotent and also works in an archived
// Project; each successful request is audited.
func (s *Store) RevokeProjectExecutorCredential(ctx context.Context, project identity.Principal, environment, keyID string) error {
	if err := s.selfHostedExecutorTarget(ctx, project, environment); err != nil {
		return err
	}
	if err := s.exactExecutorRestriction(ctx, project, environment, keyID); err != nil {
		return err
	}
	tenant, id, err := executorCredentialIdentity(project, keyID)
	if err != nil {
		return err
	}
	record := executorCredentialAudit(project, "revoke", keyID)
	return s.pooled.Transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		n, err := q.RevokeExecutorCredential(ctx, sqlc.RevokeExecutorCredentialParams{KeyID: id, TenantID: tenant, SubjectKind: pgtype.Text{String: project.SubjectKind, Valid: true}, SubjectID: pgtype.Text{String: project.SubjectID, Valid: true}})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return record(ctx, q)
	})
}

func executorCredentialAudit(project identity.Principal, action, keyID string) func(context.Context, *sqlc.Queries) error {
	return func(ctx context.Context, q *sqlc.Queries) error {
		return recordAdminMutation(ctx, q, project.TenantID, action, "executor_credential", keyID)
	}
}

// activeProjectAudit repeats the archive check in the writing transaction. It
// share-locks the Project, which archiving updates, so an issuance or rotation
// either commits before the archive or sees it and fails.
func activeProjectAudit(project identity.Principal, action, keyID string) func(context.Context, *sqlc.Queries) error {
	audit := executorCredentialAudit(project, action, keyID)
	return func(ctx context.Context, q *sqlc.Queries) error {
		if err := activeProject(ctx, q, project); err != nil {
			return err
		}
		return audit(ctx, q)
	}
}

// activeProject returns ErrProjectArchived for an archived Project.
func activeProject(ctx context.Context, q *sqlc.Queries, project identity.Principal) error {
	tenant, err := parseID(project.TenantID)
	if err != nil {
		return err
	}
	row, err := q.LockProjectByTenant(ctx, tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if row.ArchivedAt.Valid {
		return ErrProjectArchived
	}
	return nil
}

func (s *Store) selfHostedExecutorTarget(ctx context.Context, principal identity.Principal, environment string) error {
	if err := principal.Validate(); err != nil {
		return ErrInvalidInput
	}
	owned, err := s.GetEnvironment(ctx, principal.TenantID, environment)
	if err != nil {
		return err
	}
	var configuration struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(owned.Configuration, &configuration) != nil || configuration.Type != "self_hosted" {
		return ErrNotFound
	}
	return nil
}

func (s *Store) exactExecutorRestriction(ctx context.Context, principal identity.Principal, environment, keyID string) error {
	tenant, id, err := executorCredentialIdentity(principal, keyID)
	if err != nil {
		return err
	}
	want := parsePathID(environment)
	actual, err := s.executorCredentialRestriction(ctx, principal, tenant, id)
	if err != nil {
		return err
	}
	// Restrictions and principals are immutable, so checking before the
	// rotation or revocation transaction cannot authorize a different target.
	if !actual.Valid || actual != want {
		return ErrNotFound
	}
	return nil
}
