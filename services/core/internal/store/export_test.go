package store

import (
	"bytes"
	"context"
	"encoding/json"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
)

func NewTestStore(t *testing.T) (*Store, *pgxpool.Pool) { return testStore(t) }

var fixtureCipher, _ = credentialcrypto.New(bytes.Repeat([]byte{61}, 32))

// FixtureCipher is the credential key of NewModelTestStore, for reopened stores.
func FixtureCipher() *credentialcrypto.Cipher { return fixtureCipher }

// NewModelTestStore is NewTestStore with a fixture credential key, so Sessions
// can freeze a model provider.
func NewModelTestStore(t *testing.T) (*Store, *pgxpool.Pool) {
	_, pool := testStore(t)
	return NewWithCredentialCipher(pool, fixtureCipher), pool
}

// FixtureModelProvider is a valid bundle for the harness. Hosted and self-hosted
// Sessions cannot run without one, so fixtures supply it instead of relaxing
// that check.
func FixtureModelProvider(harness string) *v1.ModelProviderInput {
	if harness == "codex" {
		return &v1.ModelProviderInput{Protocol: "responses", BaseURL: "https://model.fixture.example/v1", APIKey: "fixture-model-key"}
	}
	return &v1.ModelProviderInput{Protocol: "anthropic", BaseURL: "https://model.fixture.example/anthropic", APIKey: "fixture-model-key", ContextWindow: 200000, MaxOutputTokens: 8000}
}

// WithFixtureModelProvider adds the fixture provider, as a Session-supplied
// bundle, to a hosted or self-hosted creation that has none. The store must
// have a credential key; other inputs are returned unchanged.
func WithFixtureModelProvider(input CreateSessionInput) CreateSessionInput {
	var configuration struct {
		Environment struct {
			Type string `json:"type"`
		} `json:"environment"`
	}
	if input.ModelProvider != nil || json.Unmarshal(input.Configuration, &configuration) != nil || !v1.ModelProviderRequired(configuration.Environment.Type) {
		return input
	}
	input.ModelProvider, input.ModelProviderSource = FixtureModelProvider(input.Engine), v1.ModelProviderSourceSession
	if input.ExecutionConfiguration != nil {
		projection := *input.ExecutionConfiguration
		projection.ModelProvider = v1.ExecutionProviderSelection{Source: "session", Status: "available", Configuration: input.ModelProvider.SafeView()}
		input.ExecutionConfiguration = &projection
	}
	return input
}

// FixtureCreator is an explicit synthetic principal for newly created test Sessions.
func FixtureCreator() identity.Subject {
	return identity.Subject{Kind: "service_account", ID: "test-runner"}
}

// FixtureExecutorPrincipal explicitly provisions a synthetic project for executor fixtures.
func FixtureExecutorPrincipal(t *testing.T, s *Store, tenant string) identity.Principal {
	t.Helper()
	p := identity.Principal{ProjectScope: identity.ProjectScope{TenantID: tenant, OrganizationID: "test-org", ProjectID: tenant}, SubjectKind: FixtureCreator().Kind, SubjectID: FixtureCreator().ID}
	if err := s.EnsureProjectScopes(t.Context(), []identity.ProjectScope{p.ProjectScope}); err != nil {
		t.Fatal(err)
	}
	return p
}

// SkillArchive builds a minimal valid Skill archive for public HTTP fixtures.
func SkillArchive(t *testing.T, marker string) []byte { return skillArchive(t, marker) }

// CommitLegacyDeletion commits a deletion marker the way releases before the
// idle-only deletion rule did, for Sessions that public deletion now rejects.
func (s *Store) CommitLegacyDeletion(ctx context.Context, tenantID, sessionID string) error {
	return s.commitLegacyDeletion(ctx, tenantID, sessionID)
}
