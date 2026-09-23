package storeresolver

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type resolverStore struct {
	environment store.Environment
	err         error
	calls       int
}

func (s *resolverStore) GetSessionEnvironment(context.Context, string, string) (store.Environment, error) {
	s.calls++
	return s.environment, s.err
}

func TestResolverAuthorizesManagedSessionWithoutSelectingCurrentAllocation(t *testing.T) {
	backend := &resolverStore{environment: store.Environment{
		ID: environmentID, TenantID: tenantID, SessionID: sessionID,
		Configuration: []byte(`{"type":"openai_hosted"}`),
	}}
	resolver, err := NewResolver(backend)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := resolver.ResolveRuntimeHistoryScope(t.Context(), tenantID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if scope != (runtimehistory.Scope{TenantID: tenantID, SessionID: sessionID, EnvironmentID: environmentID}) || backend.calls != 1 {
		t.Fatalf("unexpected Runtime history scope: %+v calls=%d", scope, backend.calls)
	}
}

func TestResolverPreservesTenantScopedNotFound(t *testing.T) {
	backend := &resolverStore{err: store.ErrNotFound}
	resolver, err := NewResolver(backend)
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.ResolveRuntimeHistoryScope(t.Context(), tenantID, sessionID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("tenant-scoped not found was not preserved: %v", err)
	}
}

func TestResolverRejectsUnsupportedOrMismatchedEnvironment(t *testing.T) {
	for _, environment := range []store.Environment{
		{ID: environmentID, TenantID: tenantID, SessionID: sessionID, Configuration: []byte(`{"type":"self_hosted"}`)},
		{ID: environmentID, TenantID: "55555555-5555-4555-8555-555555555555", SessionID: sessionID, Configuration: []byte(`{"type":"openai_hosted"}`)},
		{ID: environmentID, TenantID: tenantID, SessionID: "66666666-6666-4666-8666-666666666666", Configuration: []byte(`{"type":"openai_hosted"}`)},
		{ID: environmentID, TenantID: tenantID, SessionID: sessionID, Configuration: []byte(`{"type":`)},
	} {
		resolver, err := NewResolver(&resolverStore{environment: environment})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := resolver.ResolveRuntimeHistoryScope(t.Context(), tenantID, sessionID); err == nil {
			t.Fatalf("unsafe Runtime history Environment accepted: %+v", environment)
		}
	}
}

const (
	tenantID      = "11111111-1111-4111-8111-111111111111"
	sessionID     = "22222222-2222-4222-8222-222222222222"
	environmentID = "33333333-3333-4333-8333-333333333333"
)
