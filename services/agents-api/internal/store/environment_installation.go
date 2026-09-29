package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrInstallationAuthorization = errors.New("installation authorization is invalid or expired; obtain a new command from the Session")

// InstallationAuthorization permits claiming one Environment's connect-only key.
// The Environment UUID is reserved as that key's ID. Reissuing an authorization
// never rotates or revives the key, and a retry must prove the same local secret.
type InstallationAuthorization struct {
	Principal   identity.Principal `json:"principal"`
	Environment string             `json:"environment_id"`
	Version     string             `json:"version"`
	ExpiresAt   int64              `json:"expires_at"`
}

func (s *Store) AuthorizeEnvironmentInstallation(ctx context.Context, principal identity.Principal, environment, version string) (string, int64, error) {
	if err := s.selfHostedExecutorTarget(ctx, principal, environment); err != nil {
		return "", 0, err
	}
	if err := activeProject(ctx, s.queries, principal); err != nil {
		return "", 0, err
	}
	claim := InstallationAuthorization{Principal: principal, Environment: environment, Version: version, ExpiresAt: time.Now().Add(30 * time.Minute).Unix()}
	payload, err := json.Marshal(claim)
	if err != nil {
		return "", 0, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	signature, err := s.credentialCipher.Fingerprint("environment-installation", encoded)
	if err != nil {
		return "", 0, err
	}
	return encoded + "." + signature, claim.ExpiresAt, nil
}

func (s *Store) ValidateEnvironmentInstallation(ctx context.Context, token, version string) (InstallationAuthorization, error) {
	var claim InstallationAuthorization
	encoded, signature, ok := strings.Cut(token, ".")
	if !ok || len(token) > 4096 {
		return claim, ErrInstallationAuthorization
	}
	want, err := s.credentialCipher.Fingerprint("environment-installation", encoded)
	if err != nil || !hmac.Equal([]byte(want), []byte(signature)) {
		return claim, ErrInstallationAuthorization
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || json.Unmarshal(payload, &claim) != nil || claim.Version != version || claim.ExpiresAt <= time.Now().Unix() {
		return InstallationAuthorization{}, ErrInstallationAuthorization
	}
	if err := s.selfHostedExecutorTarget(ctx, claim.Principal, claim.Environment); err != nil {
		return InstallationAuthorization{}, ErrInstallationAuthorization
	}
	if err := activeProject(ctx, s.queries, claim.Principal); err != nil {
		return InstallationAuthorization{}, ErrInstallationAuthorization
	}
	return claim, nil
}

// ClaimEnvironmentInstallation stores only the digest of a secret generated and
// persisted by the installer before this request. A lost HTTP response is safe
// to retry; another machine or a revoked/rotated key cannot claim it again.
func (s *Store) ClaimEnvironmentInstallation(ctx context.Context, token, version, secret string) error {
	claim, err := s.ValidateEnvironmentInstallation(ctx, token, version)
	if err != nil {
		return err
	}
	decoded, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != secret {
		return ErrInvalidInput
	}
	principal := claim.Principal
	tenant, id, err := executorCredentialIdentity(principal, claim.Environment)
	if err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(secret))
	digest := hex.EncodeToString(hash[:])
	return s.withExecutorCredentialTarget(ctx, principal, id, func(ctx context.Context, q *sqlc.Queries) error {
		if err := activeProject(ctx, q, principal); err != nil {
			return err
		}
		keys, err := q.ListEnvironmentExecutorCredentials(ctx, sqlc.ListEnvironmentExecutorCredentialsParams{TenantID: tenant, EnvironmentID: id, SubjectKind: pgtype.Text{String: principal.SubjectKind, Valid: true}, SubjectID: pgtype.Text{String: principal.SubjectID, Valid: true}})
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			// Existing operator-issued keys must not be replaced by onboarding.
			if len(keys) != 1 || keys[0].KeyID != id || keys[0].RevokedAt.Valid {
				return ErrExecutorCredentialExists
			}
			_, err := q.AuthenticateEnvironmentExecutor(ctx, sqlc.AuthenticateEnvironmentExecutorParams{EnvironmentID: id, TokenSha256: digest})
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrExecutorCredentialExists
			}
			return err
		}
		_, err = q.IssueExecutorCredential(ctx, sqlc.IssueExecutorCredentialParams{KeyID: id, TenantID: tenant, SubjectKind: pgtype.Text{String: principal.SubjectKind, Valid: true}, SubjectID: pgtype.Text{String: principal.SubjectID, Valid: true}, OrganizationID: principal.OrganizationID, ProjectID: principal.ProjectID, EnvironmentID: id, TokenSha256: digest})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrExecutorCredentialExists
		}
		return err
	})
}
