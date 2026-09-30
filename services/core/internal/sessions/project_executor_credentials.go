package sessions

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
)

// The Project executor credential use cases serve Core-key credential
// management. project is the Project's own principal, which becomes the
// credential's execution principal; the target must be a self_hosted
// Environment of that Project whose Session is not deleted, and only
// credentials restricted to it are managed. Each write records an
// administrator audit entry in its transaction, under the provenance in ctx.

// IssueProjectExecutorCredential issues a new key restricted to the
// Environment or, with rotate, replaces the secret of an existing key
// restricted to it. The target is checked first, then the archived Project
// (projects.ErrArchived), then the key.
func (s *Service) IssueProjectExecutorCredential(ctx context.Context, project identity.Principal, environment, key string, rotate bool) (IssuedExecutorCredential, error) {
	target, err := s.selfHostedTarget(ctx, project, environment)
	if err != nil {
		return IssuedExecutorCredential{}, err
	}
	if err := s.checkActiveProject(ctx, project.TenantID); err != nil {
		return IssuedExecutorCredential{}, err
	}
	if !rotate {
		return s.issueExecutorCredential(ctx, project, key, target.ID, auditActiveProject("issue", key))
	}
	if err := s.checkRestrictedTo(ctx, project, target, key); err != nil {
		return IssuedExecutorCredential{}, err
	}
	return s.rotateExecutorCredential(ctx, project, key, auditActiveProject("rotate", key))
}

// RevokeProjectExecutorCredential revokes a key restricted to the
// Environment. Revoking it again succeeds, and so does revoking in an archived
// Project.
func (s *Service) RevokeProjectExecutorCredential(ctx context.Context, project identity.Principal, environment, key string) error {
	target, err := s.selfHostedTarget(ctx, project, environment)
	if err != nil {
		return err
	}
	if err := s.checkRestrictedTo(ctx, project, target, key); err != nil {
		return err
	}
	return s.storage.WithExecutorCredentials(ctx, project.TenantID, func(ctx context.Context, tx ExecutorCredentialTx) error {
		if err := tx.RevokeExecutorCredential(ctx, project.Subject(), key); err != nil {
			return err
		}
		return tx.RecordExecutorCredentialAudit(ctx, "revoke", key)
	})
}

// auditActiveProject repeats the archive check in the writing transaction,
// whose share lock on the Project makes an issuance or rotation either commit
// before an archive or see it, then records the audit entry.
func auditActiveProject(action, key string) func(context.Context, ExecutorCredentialTx) error {
	return func(ctx context.Context, tx ExecutorCredentialTx) error {
		if err := lockActiveProject(ctx, tx); err != nil {
			return err
		}
		return tx.RecordExecutorCredentialAudit(ctx, action, key)
	}
}

// checkRestrictedTo confirms that the Project's key is restricted to exactly
// the target; any other key is ErrNotFound. Restrictions and principals never
// change, so checking before the rotation or revocation transaction cannot
// authorize another target.
func (s *Service) checkRestrictedTo(ctx context.Context, project identity.Principal, target Environment, key string) error {
	if err := checkExecutorIdentity(project, key); err != nil {
		return err
	}
	restriction, err := s.storage.LoadExecutorCredentialRestriction(ctx, project, key)
	if err != nil {
		return err
	}
	if restriction != target.ID {
		return ErrNotFound
	}
	return nil
}
