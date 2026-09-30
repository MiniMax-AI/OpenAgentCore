package api

import (
	"context"
	"crypto/sha256"
	"errors"
)

// projectKeyDigests reports whether any Project API key, revoked or not, has
// a secret digest.
type projectKeyDigests interface {
	APIKeyDigestExists(context.Context, [sha256.Size]byte) (bool, error)
}

// ValidateCredentialSeparation rejects Core key collisions with persisted API keys.
func ValidateCredentialSeparation(ctx context.Context, admin *DeploymentAuthenticator, keys projectKeyDigests) error {
	if admin == nil {
		return errors.New("OAC_CORE_KEY_DIGESTS_FILE is required; Core needs the Core key digest")
	}
	for digest := range admin.digests {
		exists, err := keys.APIKeyDigestExists(ctx, digest)
		if err != nil {
			return err
		}
		if exists {
			return errors.New("the Core key overlaps a persisted project API key")
		}
	}
	return nil
}
