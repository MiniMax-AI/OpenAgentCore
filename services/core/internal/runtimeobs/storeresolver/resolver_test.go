package storeresolver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

type resolverStore struct {
	session       sessions.Session
	measured      json.RawMessage
	allocation    store.RuntimeAllocation
	allocationErr error
	page          store.RuntimeObservationSessionPage
}

func (s resolverStore) GetSession(context.Context, string, string) (sessions.Session, error) {
	return s.session, nil
}

func (s resolverStore) MeasuredSessionUsage(_ context.Context, tenant, session string) (json.RawMessage, error) {
	if tenant != s.session.TenantID || session != s.session.ID {
		return nil, errors.New("measured usage read for another Session")
	}
	return s.measured, nil
}

func (s resolverStore) GetRuntimeAllocation(context.Context, string, string) (store.RuntimeAllocation, error) {
	return s.allocation, s.allocationErr
}

func (s resolverStore) ListRuntimeObservationSessions(context.Context, string, int) (store.RuntimeObservationSessionPage, error) {
	return s.page, nil
}

func TestResolverListsOnlyProviderNeutralSessionIdentity(t *testing.T) {
	r, err := NewResolver(resolverStore{page: store.RuntimeObservationSessionPage{
		Sessions:   []store.RuntimeObservationSession{{TenantID: "tenant", SessionID: "session"}},
		NextCursor: "session",
	}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.ListRuntimeObservationSessions(t.Context(), "", 32)
	if err != nil || len(page.Sessions) != 1 || page.Sessions[0].TenantID != "tenant" || page.Sessions[0].SessionID != "session" || page.NextCursor != "session" {
		t.Fatalf("unexpected observation scan identity: %+v %v", page, err)
	}
}

func TestResolverBindsManagedSessionEnvironmentAndAllocation(t *testing.T) {
	r, err := NewResolver(resolverStore{
		session:  sessions.Session{ID: "session", TenantID: "tenant", Configuration: []byte(`{"environment":{"type":"openai_hosted"}}`), Environment: &sessions.Environment{ID: "environment", TenantID: "tenant", SessionID: "session"}},
		measured: []byte(`{"input_tokens":120,"input_tokens_details":{"cached_tokens":20},"output_tokens":30,"output_tokens_details":{"reasoning_tokens":10},"total_tokens":150}`),
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
	if target.TenantID != "tenant" || target.SessionID != "session" || target.EnvironmentID != "environment" || target.Mode != runtimeobs.ModeManaged || target.Instance.AllocationID != "allocation" || target.Instance.ProviderKey != "provider" || target.Instance.DeviceID != "device" {
		t.Fatalf("incorrect managed identity binding: %+v", target)
	}
	if string(target.Instance.ProviderState) != `{"current":{"name":"sandbox"}}` {
		t.Fatalf("provider state was not retained: %s", target.Instance.ProviderState)
	}
	if target.Instance.ComputePhase != "running" {
		t.Fatalf("compute phase was not retained: %s", target.Instance.ComputePhase)
	}
	if target.TokenUsage == nil || target.TokenUsage.InputTokens != 120 || target.TokenUsage.OutputTokens != 30 {
		t.Fatalf("measured Session usage was not retained: %+v", target.TokenUsage)
	}
}

func TestResolverRejectsInvalidCanonicalSessionUsage(t *testing.T) {
	for _, usage := range []string{
		`{"input_tokens":2,"input_tokens_details":{"cached_tokens":0},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":4}`,
		`{}`,
		`{"total_tokens":0}`,
		`{"input_tokens":0,"input_tokens_details":{"cached_tokens":-1},"output_tokens":0,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":0}`,
		`{"input_tokens":0,"input_tokens_details":{"cached_tokens":0},"output_tokens":0,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":0,"unknown":0}`,
	} {
		resolver, err := NewResolver(resolverStore{session: sessions.Session{
			ID: "session", TenantID: "tenant", Configuration: []byte(`{"environment":{"type":"none"}}`),
		}, measured: []byte(usage)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := resolver.Resolve(t.Context(), "tenant", "session"); err == nil {
			t.Fatalf("invalid Session token usage was accepted: %s", usage)
		}
	}
}

func TestResolverKeepsNullCanonicalSessionUsageAbsent(t *testing.T) {
	resolver, err := NewResolver(resolverStore{session: sessions.Session{
		ID: "session", TenantID: "tenant", Configuration: []byte(`{"environment":{"type":"none"}}`),
	}, measured: []byte(" \n null \t")})
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolver.Resolve(t.Context(), "tenant", "session")
	if err != nil || target.TokenUsage != nil {
		t.Fatalf("null Session usage was not kept absent: %+v %v", target.TokenUsage, err)
	}
}

// Telemetry reads measured usage, not the public Session value, which stays
// null while a root Turn runs.
func TestResolverUsesMeasuredRatherThanPublicSessionUsage(t *testing.T) {
	measured := `{"input_tokens":7,"input_tokens_details":{"cached_tokens":0},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":10}`
	for _, test := range []struct {
		public, measured string
		want             *runtimeobs.TokenUsage
	}{
		{"null", measured, &runtimeobs.TokenUsage{InputTokens: 7, OutputTokens: 3}},
		{measured, "null", nil},
	} {
		resolver, err := NewResolver(resolverStore{session: sessions.Session{
			ID: "session", TenantID: "tenant", Configuration: []byte(`{"environment":{"type":"none"}}`), Usage: []byte(test.public),
		}, measured: []byte(test.measured)})
		if err != nil {
			t.Fatal(err)
		}
		target, err := resolver.Resolve(t.Context(), "tenant", "session")
		if err != nil || (target.TokenUsage == nil) != (test.want == nil) || (test.want != nil && *target.TokenUsage != *test.want) {
			t.Fatalf("usage source: %+v %v", target.TokenUsage, err)
		}
	}
}

func TestResolverKeepsUnsupportedModesDistinct(t *testing.T) {
	for _, tc := range []struct {
		mode        string
		environment *sessions.Environment
	}{
		{mode: "none"},
		{mode: "self_hosted", environment: &sessions.Environment{ID: "environment", TenantID: "tenant", SessionID: "session"}},
	} {
		r, err := NewResolver(resolverStore{session: sessions.Session{ID: "session", TenantID: "tenant", Configuration: []byte(`{"environment":{"type":"` + tc.mode + `"}}`), Environment: tc.environment}})
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
		session:       sessions.Session{ID: "session", TenantID: "tenant", Configuration: []byte(`{"environment":{"type":"openai_hosted"}}`), Environment: &sessions.Environment{ID: "environment", TenantID: "tenant", SessionID: "session"}},
		allocationErr: sessions.ErrNotFound,
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := r.Resolve(t.Context(), "tenant", "session")
	if !errors.Is(err, runtimeobs.ErrUnavailable) || target.EnvironmentID != "environment" || target.Mode != runtimeobs.ModeManaged {
		t.Fatalf("allocation absence was not preserved: %+v %v", target, err)
	}
}

func TestResolverRejectsMismatchedEnvironmentOwnership(t *testing.T) {
	for _, mode := range []string{"self_hosted", "openai_hosted"} {
		for _, environment := range []sessions.Environment{
			{ID: "environment", TenantID: "other", SessionID: "session"},
			{ID: "environment", TenantID: "tenant", SessionID: "other"},
		} {
			resolver, err := NewResolver(resolverStore{session: sessions.Session{
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
			session: sessions.Session{
				ID: "session", TenantID: "tenant", Configuration: []byte(`{"environment":{"type":"openai_hosted"}}`),
				Environment: &sessions.Environment{ID: "environment", TenantID: "tenant", SessionID: "session"},
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
