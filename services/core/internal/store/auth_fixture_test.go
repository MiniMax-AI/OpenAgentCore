package store_test

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

type testAPIKey struct{ Name, TokenSHA256, TenantID, OrganizationID, ProjectID, SubjectKind, SubjectID string }
type fixtureKeyResolver map[string]store.ProjectAPIKeyBinding

func (f fixtureKeyResolver) ResolveProjectAPIKey(_ context.Context, digest string) (store.ProjectAPIKeyBinding, error) {
	if b, ok := f[digest]; ok {
		return b, nil
	}
	return store.ProjectAPIKeyBinding{}, store.ErrNotFound
}

// newTestAuthenticator binds each key's digest to its Principal, for
// publicHandler.
func newTestAuthenticator(t testing.TB, keys []testAPIKey) fixtureKeyResolver {
	t.Helper()
	resolver := fixtureKeyResolver{}
	for _, k := range keys {
		p := identity.Principal{ProjectScope: identity.ProjectScope{TenantID: k.TenantID, OrganizationID: k.OrganizationID, ProjectID: k.ProjectID}, SubjectKind: k.SubjectKind, SubjectID: k.SubjectID}
		if err := p.Validate(); err != nil {
			t.Fatalf("invalid fixture principal: %v", err)
		}
		if digest, err := hex.DecodeString(k.TokenSHA256); err != nil || len(digest) != 32 {
			t.Fatalf("invalid fixture digest %q", k.TokenSHA256)
		}
		if _, exists := resolver[k.TokenSHA256]; exists {
			t.Fatalf("duplicate fixture digest %q", k.TokenSHA256)
		}
		resolver[k.TokenSHA256] = store.ProjectAPIKeyBinding{Key: store.ProjectAPIKey{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(k.TokenSHA256)).String(), Name: k.Name, Prefix: "pc_" + k.TokenSHA256[:8]}, Principal: p}
	}
	return resolver
}
