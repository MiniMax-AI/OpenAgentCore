package api

import (
	"context"
	"encoding/hex"
	"errors"
)

type projectKeySeparationStore interface {
	ValidateProjectKeySeparation(context.Context, []string, []string) error
}

// ValidateCredentialSeparation rejects credential and tenant collisions before serving requests.
func ValidateCredentialSeparation(ctx context.Context, auth *Authenticator, admin *DeploymentAuthenticator, s projectKeySeparationStore) error {
	digests := []string{}
	tenants := []string{}
	seenTenants := map[string]bool{}
	for digest, p := range auth.principals {
		if seenTenants[p.TenantID] {
			return errors.New("each static API key must have its own tenant")
		}
		seenTenants[p.TenantID] = true
		if admin != nil {
			if _, ok := admin.digests[digest]; ok {
				return errors.New("administrator credentials must differ from API keys")
			}
		}
		digests = append(digests, hex.EncodeToString(digest[:]))
		tenants = append(tenants, p.TenantID)
	}
	if admin != nil {
		for digest := range admin.digests {
			digests = append(digests, hex.EncodeToString(digest[:]))
		}
	}
	if len(auth.principals) == 0 && admin == nil {
		return errors.New("an administrator credential is required when no static API keys are configured")
	}
	return s.ValidateProjectKeySeparation(ctx, digests, tenants)
}
