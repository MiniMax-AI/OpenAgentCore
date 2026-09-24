package api

import (
	"context"
	"encoding/hex"
	"errors"
)

type projectKeySeparationStore interface {
	ValidateProjectKeySeparation(context.Context, []string) error
}

// ValidateCredentialSeparation rejects administrator collisions with persisted API keys.
func ValidateCredentialSeparation(ctx context.Context, admin *DeploymentAuthenticator, s projectKeySeparationStore) error {
	if admin == nil {
		return errors.New("an administrator credential is required")
	}
	digests := make([]string, 0, len(admin.digests))
	for digest := range admin.digests {
		digests = append(digests, hex.EncodeToString(digest[:]))
	}
	return s.ValidateProjectKeySeparation(ctx, digests)
}
