package sessions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
)

// ExecutorCredentialReader authenticates Environment executor credentials and
// reads a Project's credentials for an Environment.
type ExecutorCredentialReader interface {
	// AuthenticateEnvironmentExecutor checks, in one database snapshot, that
	// the credential whose SHA-256 digest is given is current for the
	// Environment and belongs to its Session's creator, and returns the
	// Session's tenant. Any other credential is ErrNotFound.
	AuthenticateEnvironmentExecutor(ctx context.Context, environment, digest string) (string, error)
	// ProjectExecutorCredentialState reads, in one snapshot, the connection
	// facts of the tenant's self_hosted Environment and the metadata of the
	// credentials restricted to it that the Project's principal holds, oldest
	// first. An invalid principal is ErrInvalidInput; an Environment of a
	// publicly deleted Session, another type or another tenant is ErrNotFound.
	// The snapshot ends before any live observation of the connection.
	ProjectExecutorCredentialState(ctx context.Context, project identity.Principal, environment string) (ExecutorCredentialState, error)
}

type IssuedExecutorCredential struct {
	KeyID         string `json:"key_id"`
	EnvironmentID string `json:"environment_id,omitempty"`
	Token         string `json:"executor_token"`
}

// ExecutorCredential is the metadata of one Environment executor credential.
// Its secret is returned only when issued or rotated.
type ExecutorCredential struct {
	KeyID     string     `json:"key_id" format:"uuid"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at" extensions:"x-nullable"`
}

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

// ExecutorCredentialGrant is a new executor credential: its key, the principal
// it executes as, the Environment it is restricted to, empty for none, and the
// SHA-256 digest of its secret.
type ExecutorCredentialGrant struct {
	Principal     identity.Principal
	KeyID         string
	EnvironmentID string
	Digest        string
}

// ExecutorCredentialStorage stores Environment executor credentials and signs
// installation authorizations.
type ExecutorCredentialStorage interface {
	// GetEnvironment reads the tenant's Environment of a Session that was not
	// publicly deleted, as EnvironmentReader does.
	GetEnvironment(ctx context.Context, tenant, environment string) (Environment, error)
	// LoadProjectArchived reports whether the tenant's Project is archived. A
	// tenant without a Project is ErrNotFound.
	LoadProjectArchived(ctx context.Context, tenant string) (bool, error)
	// ExecutorProjectExists reports whether the scope's organization and
	// Project map to its execution tenant.
	ExecutorProjectExists(ctx context.Context, scope identity.ProjectScope) (bool, error)
	// LoadExecutorCredentialRestriction reads the Environment the principal's
	// key is restricted to, empty for an unrestricted key. A key of another
	// principal or Project, or an unknown one, is ErrNotFound.
	LoadExecutorCredentialRestriction(ctx context.Context, principal identity.Principal, key string) (string, error)
	// SignInstallation returns the signature of an installation authorization
	// payload under the credential key. Without that key it is
	// credentialcrypto.ErrUnavailable.
	SignInstallation(ctx context.Context, payload string) (string, error)
	// VerifyInstallation checks the signature of an installation
	// authorization payload: another signature is
	// ErrInstallationAuthorization, and a service without the credential key
	// credentialcrypto.ErrUnavailable.
	VerifyInstallation(ctx context.Context, payload, signature string) error
	// WithExecutorCredentials runs apply in a transaction of the tenant.
	WithExecutorCredentials(ctx context.Context, tenant string, apply func(context.Context, ExecutorCredentialTx) error) error
	// WithEnvironmentExecutorCredentials reads the tenant's Environment, then
	// runs apply in the transaction of its Session, with what the Session lock
	// shows. A malformed Environment ID is ErrInvalidInput, and an Environment
	// of a publicly deleted Session, or a missing one, ErrNotFound.
	WithEnvironmentExecutorCredentials(ctx context.Context, tenant, environment string, apply func(context.Context, EnvironmentExecutorCredentialTx, LockedSession) error) error
}

// ExecutorCredentialTx is the transaction of one tenant that executor
// credentials are written in.
type ExecutorCredentialTx interface {
	// LockProject share-locks the tenant's Project, which archiving updates,
	// and reports whether it is archived, so a write either commits before the
	// archive or sees it. A tenant without a Project is ErrNotFound.
	LockProject(ctx context.Context) (bool, error)
	// ListExecutorCredentials lists the subject's keys restricted to the
	// Environment, oldest first.
	ListExecutorCredentials(ctx context.Context, environment string, subject identity.Subject) ([]ExecutorCredential, error)
	// AuthenticateExecutor reports whether the secret with the SHA-256 digest
	// authenticates for the Environment, as AuthenticateEnvironmentExecutor
	// checks.
	AuthenticateExecutor(ctx context.Context, environment, digest string) (bool, error)
	// IssueExecutorCredential stores the new credential in its principal's
	// Project and returns its key and restriction. When no credential is
	// stored, because the key ID exists or the restriction names an
	// Environment the principal did not create, it is
	// ErrExecutorCredentialExists.
	IssueExecutorCredential(ctx context.Context, grant ExecutorCredentialGrant) (IssuedExecutorCredential, error)
	// RotateExecutorCredential replaces the secret of the subject's key with
	// the one whose SHA-256 digest is given, restores the key if it was
	// revoked, and returns its key and restriction. An unknown key is
	// ErrNotFound.
	RotateExecutorCredential(ctx context.Context, subject identity.Subject, key, digest string) (IssuedExecutorCredential, error)
	// RevokeExecutorCredential revokes the subject's key; revoking it again
	// changes nothing. An unknown key is ErrNotFound.
	RevokeExecutorCredential(ctx context.Context, subject identity.Subject, key string) error
	// RecordExecutorCredentialAudit records the administrator audit entry of
	// the action on the key under the provenance in ctx. It never records a
	// secret.
	RecordExecutorCredentialAudit(ctx context.Context, action, key string) error
}

// EnvironmentExecutorCredentialTx is the transaction of the Session whose
// Environment a credential is restricted to.
type EnvironmentExecutorCredentialTx interface {
	ExecutorCredentialTx
	// LoadSessionCreator reads who created the Session and reports whether
	// the Session recorded a creator.
	LoadSessionCreator(ctx context.Context) (identity.Subject, bool, error)
}

// executorSecret is a new connect-only secret and the SHA-256 digest that is
// stored instead of it.
type executorSecret struct{ token, digest string }

// newExecutorSecret generates 32 random bytes as an unpadded base64url token.
func newExecutorSecret() (executorSecret, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return executorSecret{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	return executorSecret{token: token, digest: executorDigest(token)}, nil
}

func executorDigest(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// installationSecretDigest returns the digest of a secret an installer
// generated: exactly 32 bytes in canonical unpadded base64url. Anything else is
// ErrInvalidInput.
func installationSecretDigest(secret string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != secret {
		return "", ErrInvalidInput
	}
	return executorDigest(secret), nil
}

// checkExecutorIdentity validates the principal a credential executes as and
// the credential's key ID.
func checkExecutorIdentity(principal identity.Principal, key string) error {
	if err := principal.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if !validID(key) {
		return ErrInvalidInput
	}
	return nil
}

// activeProject is projects.ErrArchived for an archived Project.
func activeProject(archived bool) error {
	if archived {
		return projects.ErrArchived
	}
	return nil
}

// lockActiveProject repeats the archive check in the writing transaction.
func lockActiveProject(ctx context.Context, tx ExecutorCredentialTx) error {
	archived, err := tx.LockProject(ctx)
	if err != nil {
		return err
	}
	return activeProject(archived)
}

// checkActiveProject reads, before any transaction, whether the tenant's
// Project is archived.
func (s *Service) checkActiveProject(ctx context.Context, tenant string) error {
	archived, err := s.storage.LoadProjectArchived(ctx, tenant)
	if err != nil {
		return err
	}
	return activeProject(archived)
}

// selfHostedTarget reads the principal's self_hosted Environment an executor
// credential or installation targets. Another type is ErrNotFound.
func (s *Service) selfHostedTarget(ctx context.Context, principal identity.Principal, environment string) (Environment, error) {
	if err := principal.Validate(); err != nil {
		return Environment{}, ErrInvalidInput
	}
	current, err := s.storage.GetEnvironment(ctx, principal.TenantID, environment)
	if err != nil {
		return Environment{}, err
	}
	if kind, err := EnvironmentType(current.Configuration); err != nil || kind != "self_hosted" {
		return Environment{}, ErrNotFound
	}
	return current, nil
}

// withExecutorTarget runs apply in the transaction that writes a credential
// restricted to environment, or unrestricted when it is empty. A restricted
// credential is written in the transaction of the Environment's Session, whose
// lock orders it against the Session's deletion, and only by the Session's
// creator; any other Session is ErrNotFound.
func (s *Service) withExecutorTarget(ctx context.Context, principal identity.Principal, environment string, apply func(context.Context, ExecutorCredentialTx) error) error {
	if environment == "" {
		return s.storage.WithExecutorCredentials(ctx, principal.TenantID, apply)
	}
	return s.storage.WithEnvironmentExecutorCredentials(ctx, principal.TenantID, environment, func(ctx context.Context, tx EnvironmentExecutorCredentialTx, locked LockedSession) error {
		if err := locked.Public(); err != nil {
			return err
		}
		creator, recorded, err := tx.LoadSessionCreator(ctx)
		if err != nil {
			return err
		}
		if !recorded || creator != principal.Subject() {
			return ErrNotFound
		}
		return apply(ctx, tx)
	})
}

// IssueExecutorCredential returns a new connect-only secret once, restricted
// to the Environment unless it is empty, without replacing an existing key:
// that is ErrExecutorCredentialExists. The principal's Project must be mapped
// to its tenant.
func (s *Service) IssueExecutorCredential(ctx context.Context, principal identity.Principal, key, environment string) (IssuedExecutorCredential, error) {
	return s.issueExecutorCredential(ctx, principal, key, environment, nil)
}

// issueExecutorCredential runs before, when given, first in the issuing
// transaction; its error aborts the issuance.
func (s *Service) issueExecutorCredential(ctx context.Context, principal identity.Principal, key, environment string, before func(context.Context, ExecutorCredentialTx) error) (IssuedExecutorCredential, error) {
	if err := checkExecutorIdentity(principal, key); err != nil {
		return IssuedExecutorCredential{}, err
	}
	exists, err := s.storage.ExecutorProjectExists(ctx, principal.ProjectScope)
	if err != nil {
		return IssuedExecutorCredential{}, err
	}
	if !exists {
		return IssuedExecutorCredential{}, ErrNotFound
	}
	if environment != "" && !validID(environment) {
		return IssuedExecutorCredential{}, ErrInvalidInput
	}
	secret, err := newExecutorSecret()
	if err != nil {
		return IssuedExecutorCredential{}, err
	}
	var result IssuedExecutorCredential
	err = s.withExecutorTarget(ctx, principal, environment, func(ctx context.Context, tx ExecutorCredentialTx) error {
		if before != nil {
			if err := before(ctx, tx); err != nil {
				return err
			}
		}
		issued, err := tx.IssueExecutorCredential(ctx, ExecutorCredentialGrant{Principal: principal, KeyID: key, EnvironmentID: environment, Digest: secret.digest})
		if err != nil {
			return err
		}
		issued.Token = secret.token
		result = issued
		return nil
	})
	if err != nil {
		return IssuedExecutorCredential{}, err
	}
	return result, nil
}

// RotateExecutorCredential returns a new secret for the principal's key once,
// keeping its restriction and restoring it if it was revoked. An unknown key
// is ErrNotFound.
func (s *Service) RotateExecutorCredential(ctx context.Context, principal identity.Principal, key string) (IssuedExecutorCredential, error) {
	return s.rotateExecutorCredential(ctx, principal, key, nil)
}

// rotateExecutorCredential runs before, when given, first in the rotating
// transaction; its error aborts the rotation.
func (s *Service) rotateExecutorCredential(ctx context.Context, principal identity.Principal, key string, before func(context.Context, ExecutorCredentialTx) error) (IssuedExecutorCredential, error) {
	if err := checkExecutorIdentity(principal, key); err != nil {
		return IssuedExecutorCredential{}, err
	}
	restriction, err := s.storage.LoadExecutorCredentialRestriction(ctx, principal, key)
	if err != nil {
		return IssuedExecutorCredential{}, err
	}
	secret, err := newExecutorSecret()
	if err != nil {
		return IssuedExecutorCredential{}, err
	}
	var result IssuedExecutorCredential
	err = s.withExecutorTarget(ctx, principal, restriction, func(ctx context.Context, tx ExecutorCredentialTx) error {
		if before != nil {
			if err := before(ctx, tx); err != nil {
				return err
			}
		}
		rotated, err := tx.RotateExecutorCredential(ctx, principal.Subject(), key, secret.digest)
		if err != nil {
			return err
		}
		rotated.Token = secret.token
		result = rotated
		return nil
	})
	if err != nil {
		return IssuedExecutorCredential{}, err
	}
	return result, nil
}

// RevokeExecutorCredential revokes the principal's key; revoking it again
// succeeds. An unknown key is ErrNotFound.
func (s *Service) RevokeExecutorCredential(ctx context.Context, principal identity.Principal, key string) error {
	if err := checkExecutorIdentity(principal, key); err != nil {
		return err
	}
	if _, err := s.storage.LoadExecutorCredentialRestriction(ctx, principal, key); err != nil {
		return err
	}
	return s.storage.WithExecutorCredentials(ctx, principal.TenantID, func(ctx context.Context, tx ExecutorCredentialTx) error {
		return tx.RevokeExecutorCredential(ctx, principal.Subject(), key)
	})
}
