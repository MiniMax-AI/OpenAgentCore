package main

import (
	"context"
	"encoding/hex"
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
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
func newTestAuthenticator(keys []testAPIKey) (*api.Authenticator, error) {
	resolver := fixtureKeyResolver{}
	for _, k := range keys {
		p := identity.Principal{ProjectScope: identity.ProjectScope{TenantID: k.TenantID, OrganizationID: k.OrganizationID, ProjectID: k.ProjectID}, SubjectKind: k.SubjectKind, SubjectID: k.SubjectID}
		if err := p.Validate(); err != nil {
			return nil, err
		}
		digest, err := hex.DecodeString(k.TokenSHA256)
		if err != nil || len(digest) != 32 {
			return nil, errors.New("invalid fixture digest")
		}
		if _, exists := resolver[k.TokenSHA256]; exists {
			return nil, errors.New("duplicate fixture digest")
		}
		resolver[k.TokenSHA256] = store.ProjectAPIKeyBinding{Key: store.ProjectAPIKey{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(k.TokenSHA256)).String(), Name: k.Name, Prefix: "pc_" + k.TokenSHA256[:8]}, Principal: p}
	}
	return api.NewDatabaseAuthenticator(resolver)
}
