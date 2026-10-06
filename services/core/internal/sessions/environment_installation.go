package sessions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
)

// InstallationAuthorization permits claiming one Environment's connect-only key.
// The Environment UUID is reserved as that key's ID. Reissuing an authorization
// never rotates or revives the key, and a retry must prove the same local secret.
type InstallationAuthorization struct {
	Principal   identity.Principal `json:"principal"`
	Environment string             `json:"environment_id"`
	Version     string             `json:"version"`
	ExpiresAt   int64              `json:"expires_at"`
}

const (
	// installationLifetime is how long an installation authorization lasts.
	installationLifetime = 30 * time.Minute
	// maxInstallationToken bounds the installation token a request presents.
	maxInstallationToken = 4096
)

// AuthorizeEnvironmentInstallation authorizes the installer of version to
// claim the connect-only key of the principal's self_hosted Environment, and
// returns the token and when it expires, in Unix seconds. The token is the
// base64url JSON authorization and its signature, joined by a dot. An archived
// Project gets none (projects.ErrArchived).
func (s *Service) AuthorizeEnvironmentInstallation(ctx context.Context, principal identity.Principal, environment, version string) (string, int64, error) {
	if _, err := s.selfHostedTarget(ctx, principal, environment); err != nil {
		return "", 0, err
	}
	if err := s.checkActiveProject(ctx, principal.TenantID); err != nil {
		return "", 0, err
	}
	claim := InstallationAuthorization{Principal: principal, Environment: environment, Version: version, ExpiresAt: time.Now().Add(installationLifetime).Unix()}
	payload, err := json.Marshal(claim)
	if err != nil {
		return "", 0, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	signature, err := s.storage.SignInstallation(ctx, encoded)
	if err != nil {
		return "", 0, err
	}
	return encoded + "." + signature, claim.ExpiresAt, nil
}

// ValidateEnvironmentInstallation returns the authorization the token carries
// while it is signed, unexpired, for version, and still names a self_hosted
// Environment of an active Project. Any other token is
// ErrInstallationAuthorization.
func (s *Service) ValidateEnvironmentInstallation(ctx context.Context, token, version string) (InstallationAuthorization, error) {
	claim, _, err := s.validateInstallation(ctx, token, version)
	return claim, err
}

// validateInstallation also returns the Environment the authorization names.
func (s *Service) validateInstallation(ctx context.Context, token, version string) (InstallationAuthorization, Environment, error) {
	encoded, signature, ok := strings.Cut(token, ".")
	if !ok || len(token) > maxInstallationToken {
		return InstallationAuthorization{}, Environment{}, ErrInstallationAuthorization
	}
	if err := s.storage.VerifyInstallation(ctx, encoded, signature); err != nil {
		return InstallationAuthorization{}, Environment{}, err
	}
	var claim InstallationAuthorization
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || json.Unmarshal(payload, &claim) != nil || claim.Version != version || claim.ExpiresAt <= time.Now().Unix() {
		return InstallationAuthorization{}, Environment{}, ErrInstallationAuthorization
	}
	target, err := s.selfHostedTarget(ctx, claim.Principal, claim.Environment)
	if err != nil {
		return InstallationAuthorization{}, Environment{}, ErrInstallationAuthorization
	}
	if err := s.checkActiveProject(ctx, claim.Principal.TenantID); err != nil {
		return InstallationAuthorization{}, Environment{}, ErrInstallationAuthorization
	}
	return claim, target, nil
}

// ClaimEnvironmentInstallation stores only the digest of a secret the
// installer generated and persisted before this request. A lost response is
// safe to retry with the same secret; another machine, or a revoked or rotated
// key, cannot claim it again (ErrExecutorCredentialExists).
func (s *Service) ClaimEnvironmentInstallation(ctx context.Context, token, version, secret string) error {
	claim, target, err := s.validateInstallation(ctx, token, version)
	if err != nil {
		return err
	}
	digest, err := installationSecretDigest(secret)
	if err != nil {
		return err
	}
	principal := claim.Principal
	// The Environment's ID is reserved as the ID of the key its installation
	// claims.
	key := target.ID
	return s.withExecutorTarget(ctx, principal, target.ID, func(ctx context.Context, tx ExecutorCredentialTx) error {
		if err := lockActiveProject(ctx, tx); err != nil {
			return err
		}
		keys, err := tx.ListExecutorCredentials(ctx, target.ID, principal.Subject())
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			// Onboarding never replaces an operator-issued key.
			if len(keys) != 1 || keys[0].KeyID != key || keys[0].RevokedAt != nil {
				return ErrExecutorCredentialExists
			}
			current, err := tx.AuthenticateExecutor(ctx, target.ID, digest)
			if err != nil {
				return err
			}
			if !current {
				return ErrExecutorCredentialExists
			}
			return nil
		}
		_, err = tx.IssueExecutorCredential(ctx, ExecutorCredentialGrant{Principal: principal, KeyID: key, EnvironmentID: target.ID, Digest: digest})
		return err
	})
}
