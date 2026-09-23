package store

import (
	"context"
	"encoding/json"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
)

// IssueEnvironmentExecutorCredential exposes only exact self-hosted targets to
// project callers. The operator issuer remains available for broader principals.
func (s *Store) IssueEnvironmentExecutorCredential(ctx context.Context, principal identity.Principal, environment, keyID string, rotate bool) (IssuedExecutorCredential, error) {
	if err := s.selfHostedExecutorTarget(ctx, principal, environment); err != nil {
		return IssuedExecutorCredential{}, err
	}
	if !rotate {
		return s.IssueExecutorCredential(ctx, principal, keyID, environment)
	}
	if err := s.exactExecutorRestriction(ctx, principal, environment, keyID); err != nil {
		return IssuedExecutorCredential{}, err
	}
	return s.RotateExecutorCredential(ctx, principal, keyID)
}

func (s *Store) RevokeEnvironmentExecutorCredential(ctx context.Context, principal identity.Principal, environment, keyID string) error {
	if err := s.selfHostedExecutorTarget(ctx, principal, environment); err != nil {
		return err
	}
	if err := s.exactExecutorRestriction(ctx, principal, environment, keyID); err != nil {
		return err
	}
	return s.RevokeExecutorCredential(ctx, principal, keyID)
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
	// Restrictions and principals are immutable, so checking before the existing
	// rotation/revocation transaction cannot authorize a different target.
	if !actual.Valid || actual != want {
		return ErrNotFound
	}
	return nil
}
