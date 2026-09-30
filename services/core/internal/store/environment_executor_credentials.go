package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// IssueExecutorCredential returns a new connect-only secret once, without replacing an existing ID.
func (s *Store) IssueExecutorCredential(ctx context.Context, principal identity.Principal, keyID, environment string) (sessions.IssuedExecutorCredential, error) {
	return s.issueExecutorCredential(ctx, principal, keyID, environment, nil)
}

// issueExecutorCredential runs before, when given, first in the issuing
// transaction; its error aborts the issuance.
func (s *Store) issueExecutorCredential(ctx context.Context, principal identity.Principal, keyID, environment string, before func(context.Context, *sqlc.Queries) error) (sessions.IssuedExecutorCredential, error) {
	tenant, id, err := executorCredentialIdentity(principal, keyID)
	if err != nil {
		return sessions.IssuedExecutorCredential{}, err
	}
	exists, err := s.queries.ExecutorProjectScopeExists(ctx, sqlc.ExecutorProjectScopeExistsParams{TenantID: tenant, OrganizationID: principal.OrganizationID, ProjectID: principal.ProjectID})
	if err != nil {
		return sessions.IssuedExecutorCredential{}, err
	}
	if !exists {
		return sessions.IssuedExecutorCredential{}, sessions.ErrNotFound
	}
	var restriction pgtype.UUID
	if environment != "" {
		restriction, err = parseID(environment)
		if err != nil {
			return sessions.IssuedExecutorCredential{}, err
		}
	}
	token, digest, err := newExecutorSecret()
	if err != nil {
		return sessions.IssuedExecutorCredential{}, err
	}
	var result sessions.IssuedExecutorCredential
	err = s.withExecutorCredentialTarget(ctx, principal, restriction, func(ctx context.Context, q *sqlc.Queries) error {
		if before != nil {
			if err := before(ctx, q); err != nil {
				return err
			}
		}
		row, err := q.IssueExecutorCredential(ctx, sqlc.IssueExecutorCredentialParams{
			KeyID: id, TenantID: tenant, SubjectKind: pgtype.Text{String: principal.SubjectKind, Valid: true}, SubjectID: pgtype.Text{String: principal.SubjectID, Valid: true},
			OrganizationID: principal.OrganizationID, ProjectID: principal.ProjectID, EnvironmentID: restriction, TokenSha256: digest,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrExecutorCredentialExists
		}
		if err != nil {
			return err
		}
		result = issuedExecutorCredential(row.KeyID, row.EnvironmentID, token)
		return nil
	})
	if err != nil {
		return sessions.IssuedExecutorCredential{}, err
	}
	return result, nil
}

func (s *Store) RotateExecutorCredential(ctx context.Context, principal identity.Principal, keyID string) (sessions.IssuedExecutorCredential, error) {
	return s.rotateExecutorCredential(ctx, principal, keyID, nil)
}

// rotateExecutorCredential runs before, when given, first in the rotating
// transaction; its error aborts the rotation.
func (s *Store) rotateExecutorCredential(ctx context.Context, principal identity.Principal, keyID string, before func(context.Context, *sqlc.Queries) error) (sessions.IssuedExecutorCredential, error) {
	tenant, id, err := executorCredentialIdentity(principal, keyID)
	if err != nil {
		return sessions.IssuedExecutorCredential{}, err
	}
	restriction, err := s.executorCredentialRestriction(ctx, principal, tenant, id)
	if err != nil {
		return sessions.IssuedExecutorCredential{}, err
	}
	token, digest, err := newExecutorSecret()
	if err != nil {
		return sessions.IssuedExecutorCredential{}, err
	}
	var result sessions.IssuedExecutorCredential
	err = s.withExecutorCredentialTarget(ctx, principal, restriction, func(ctx context.Context, q *sqlc.Queries) error {
		if before != nil {
			if err := before(ctx, q); err != nil {
				return err
			}
		}
		row, err := q.RotateExecutorCredential(ctx, sqlc.RotateExecutorCredentialParams{KeyID: id, TenantID: tenant, SubjectKind: pgtype.Text{String: principal.SubjectKind, Valid: true}, SubjectID: pgtype.Text{String: principal.SubjectID, Valid: true}, TokenSha256: digest})
		if errors.Is(err, pgx.ErrNoRows) {
			return sessions.ErrNotFound
		}
		if err != nil {
			return err
		}
		result = issuedExecutorCredential(row.KeyID, row.EnvironmentID, token)
		return nil
	})
	if err != nil {
		return sessions.IssuedExecutorCredential{}, err
	}
	return result, nil
}

func (s *Store) RevokeExecutorCredential(ctx context.Context, principal identity.Principal, keyID string) error {
	tenant, id, err := executorCredentialIdentity(principal, keyID)
	if err != nil {
		return err
	}
	if _, err := s.executorCredentialRestriction(ctx, principal, tenant, id); err != nil {
		return err
	}
	n, err := s.queries.RevokeExecutorCredential(ctx, sqlc.RevokeExecutorCredentialParams{KeyID: id, TenantID: tenant, SubjectKind: pgtype.Text{String: principal.SubjectKind, Valid: true}, SubjectID: pgtype.Text{String: principal.SubjectID, Valid: true}})
	if err == nil && n == 0 {
		return sessions.ErrNotFound
	}
	return err
}

func (s *Store) executorCredentialRestriction(ctx context.Context, principal identity.Principal, tenant, id pgtype.UUID) (pgtype.UUID, error) {
	restriction, err := s.queries.GetExecutorCredentialForPrincipal(ctx, sqlc.GetExecutorCredentialForPrincipalParams{
		KeyID: id, TenantID: tenant, SubjectKind: pgtype.Text{String: principal.SubjectKind, Valid: true}, SubjectID: pgtype.Text{String: principal.SubjectID, Valid: true},
		OrganizationID: principal.OrganizationID, ProjectID: principal.ProjectID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, sessions.ErrNotFound
	}
	return restriction, err
}

// AuthenticateEnvironmentExecutor checks the current key and recorded Session creator in one database snapshot.
func (s *Store) AuthenticateEnvironmentExecutor(ctx context.Context, environment, digest string) (string, error) {
	id, err := parseID(environment)
	if err != nil {
		return "", sessions.ErrNotFound
	}
	hash, err := hex.DecodeString(digest)
	if err != nil || len(hash) != sha256.Size {
		return "", sessions.ErrNotFound
	}
	tenant, err := s.queries.AuthenticateEnvironmentExecutor(ctx, sqlc.AuthenticateEnvironmentExecutorParams{EnvironmentID: id, TokenSha256: hex.EncodeToString(hash)})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", sessions.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("authenticate environment executor: %w", err)
	}
	return uuid.UUID(tenant.Bytes).String(), nil
}
