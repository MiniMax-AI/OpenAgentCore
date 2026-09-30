package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
	"github.com/google/uuid"
)

type testAPIKey struct{ Name, TokenSHA256, TenantID, OrganizationID, ProjectID, SubjectKind, SubjectID string }
type fixtureKeyResolver map[[sha256.Size]byte]projects.KeyBinding

func (f fixtureKeyResolver) ResolveAPIKey(_ context.Context, digest [sha256.Size]byte) (projects.KeyBinding, error) {
	if b, ok := f[digest]; ok {
		return b, nil
	}
	return projects.KeyBinding{}, projects.ErrNotFound
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
		raw, err := hex.DecodeString(k.TokenSHA256)
		if err != nil || len(raw) != sha256.Size {
			t.Fatalf("invalid fixture digest %q", k.TokenSHA256)
		}
		digest := [sha256.Size]byte(raw)
		if _, exists := resolver[digest]; exists {
			t.Fatalf("duplicate fixture digest %q", k.TokenSHA256)
		}
		resolver[digest] = projects.KeyBinding{Key: projects.APIKey{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(k.TokenSHA256)).String(), Name: k.Name, Prefix: "pc_" + k.TokenSHA256[:8]}, Principal: p}
	}
	return resolver
}
