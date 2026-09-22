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
		session: store.Session{ID: "session", TenantID: "tenant", Configuration: []byte(`{"environment":{"type":"openai_hosted"}}`), Environment: &store.Environment{ID: "environment", TenantID: "tenant", SessionID: "session"}},
		allocation: store.RuntimeAllocation{
			ID: "allocation", TenantID: "tenant", SessionID: "session", EnvironmentID: "environment",
			ProviderKey: "provider", DeviceID: "device", ComputePhase: "running", ComputeState: []byte(`{"current":{"name":"sandbox"}}`),
		},
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
	if string(target.Instance.ProviderState) != `{"current":{"name":"sandbox"}}` {
		t.Fatalf("provider state was not retained: %s", target.Instance.ProviderState)
	}
	if target.Instance.ComputePhase != "running" {
		t.Fatalf("compute phase was not retained: %s", target.Instance.ComputePhase)
	}
}

func TestResolverKeepsUnsupportedModesDistinct(t *testing.T) {
	for _, tc := range []struct {
		mode        string
		environment *store.Environment
	}{
		{mode: "none"},
		{mode: "self_hosted", environment: &store.Environment{ID: "environment", TenantID: "tenant", SessionID: "session"}},
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
		session:       store.Session{ID: "session", TenantID: "tenant", Configuration: []byte(`{"environment":{"type":"openai_hosted"}}`), Environment: &store.Environment{ID: "environment", TenantID: "tenant", SessionID: "session"}},
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

func TestResolverRejectsMismatchedEnvironmentOwnership(t *testing.T) {
	for _, mode := range []string{"self_hosted", "openai_hosted"} {
		for _, environment := range []store.Environment{
			{ID: "environment", TenantID: "other", SessionID: "session"},
			{ID: "environment", TenantID: "tenant", SessionID: "other"},
		} {
			resolver, err := NewResolver(resolverStore{session: store.Session{
				ID: "session", TenantID: "tenant",
				Configuration: []byte(`{"environment":{"type":"` + mode + `"}}`), Environment: &environment,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := resolver.Resolve(t.Context(), "tenant", "session"); err == nil {
				t.Fatalf("mismatched %s Environment accepted: %+v", mode, environment)
			}
		}
	}
}

func TestResolverRejectsMismatchedAllocationOwnership(t *testing.T) {
	base := store.RuntimeAllocation{
		ID: "allocation", TenantID: "tenant", SessionID: "session", EnvironmentID: "environment",
		ProviderKey: "provider", DeviceID: "device",
	}
	for _, mutate := range []func(*store.RuntimeAllocation){
		func(value *store.RuntimeAllocation) { value.TenantID = "other" },
		func(value *store.RuntimeAllocation) { value.SessionID = "other" },
		func(value *store.RuntimeAllocation) { value.EnvironmentID = "other" },
	} {
		allocation := base
		mutate(&allocation)
		resolver, err := NewResolver(resolverStore{
			session: store.Session{
				ID: "session", TenantID: "tenant", Configuration: []byte(`{"environment":{"type":"openai_hosted"}}`),
				Environment: &store.Environment{ID: "environment", TenantID: "tenant", SessionID: "session"},
			},
			allocation: allocation,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := resolver.Resolve(t.Context(), "tenant", "session"); err == nil {
			t.Fatalf("mismatched allocation accepted: %+v", allocation)
		}
	}
}
