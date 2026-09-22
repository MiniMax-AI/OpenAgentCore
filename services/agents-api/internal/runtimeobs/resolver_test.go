package runtimeobs

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type resolverStore struct {
	session       store.Session
	allocation    store.RuntimeAllocation
	allocationErr error
}

func (s resolverStore) GetSession(context.Context, string, string) (store.Session, error) {
	return s.session, nil
}

func (s resolverStore) GetRuntimeAllocation(context.Context, string, string) (store.RuntimeAllocation, error) {
	return s.allocation, s.allocationErr
}

func TestResolverBindsManagedSessionEnvironmentAndAllocation(t *testing.T) {
	r, err := NewResolver(resolverStore{
		session:    store.Session{ID: "session", TenantID: "tenant", Configuration: []byte(`{"environment":{"type":"openai_hosted"}}`), Environment: &store.Environment{ID: "environment"}},
		allocation: store.RuntimeAllocation{ID: "allocation", ProviderKey: "provider", DeviceID: "device"},
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := r.Resolve(t.Context(), "tenant", "session")
	if err != nil {
		t.Fatal(err)
	}
	if target.TenantID != "tenant" || target.SessionID != "session" || target.EnvironmentID != "environment" || target.Mode != ModeManaged || target.Instance.AllocationID != "allocation" || target.Instance.ProviderKey != "provider" || target.Instance.DeviceID != "device" {
		t.Fatalf("incorrect managed identity binding: %+v", target)
	}
}

func TestResolverKeepsUnsupportedModesDistinct(t *testing.T) {
	for _, tc := range []struct {
		mode        string
		environment *store.Environment
	}{
		{mode: "none"},
		{mode: "self_hosted", environment: &store.Environment{ID: "environment"}},
	} {
		r, err := NewResolver(resolverStore{session: store.Session{ID: "session", TenantID: "tenant", Configuration: []byte(`{"environment":{"type":"` + tc.mode + `"}}`), Environment: tc.environment}})
		if err != nil {
			t.Fatal(err)
		}
		target, err := r.Resolve(t.Context(), "tenant", "session")
		if err != nil || string(target.Mode) != tc.mode {
			t.Fatalf("mode %s was not resolved accurately: %+v %v", tc.mode, target, err)
		}
	}
}

func TestResolverReportsManagedAllocationAsUnavailable(t *testing.T) {
	r, err := NewResolver(resolverStore{
		session:       store.Session{ID: "session", TenantID: "tenant", Configuration: []byte(`{"environment":{"type":"openai_hosted"}}`), Environment: &store.Environment{ID: "environment"}},
		allocationErr: store.ErrNotFound,
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := r.Resolve(t.Context(), "tenant", "session")
	if !errors.Is(err, ErrUnavailable) || target.EnvironmentID != "environment" || target.Mode != ModeManaged {
		t.Fatalf("allocation absence was not preserved: %+v %v", target, err)
	}
}
