package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// resolverStore is the Session and the deployment reads one resolution sees.
type resolverStore struct {
	session       sessions.Session
	measured      json.RawMessage
	allocation    Allocation
	allocationErr error
	page          ObservationSessionPage
}

func newTestResolver(t *testing.T, s resolverStore) (*ObservationResolver, error) {
	reader := &fakeReader{t: t,
		environmentAllocation: func(context.Context, AllocationKey) (Allocation, error) { return s.allocation, s.allocationErr },
		observationSessions:   func(context.Context, string, int) (ObservationSessionPage, error) { return s.page, nil },
	}
	sessionReader := &fakeSessionReader{t: t,
		getSession: func(context.Context, string, string) (sessions.Session, error) { return s.session, nil },
		measuredSessionUsage: func(_ context.Context, tenant, session string) (json.RawMessage, error) {
			if tenant != s.session.TenantID || session != s.session.ID {
				t.Fatalf("measured usage read for another Session: %s/%s", tenant, session)
			}
			return s.measured, nil
		},
	}
	return NewObservationResolver(sessionReader, reader)
}

func TestObservationResolverListsOnlyProviderNeutralSessionIdentity(t *testing.T) {
	r, err := newTestResolver(t, resolverStore{page: ObservationSessionPage{
		Sessions:   []ObservationSession{{TenantID: "tenant", SessionID: "session"}},
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

func TestObservationResolverBindsManagedSessionEnvironmentAndAllocation(t *testing.T) {
	r, err := newTestResolver(t, resolverStore{
		session:  sessions.Session{ID: "session", TenantID: "tenant", Configuration: []byte(`{"environment":{"type":"openai_hosted"}}`), Environment: &sessions.Environment{ID: "environment", TenantID: "tenant", SessionID: "session"}},
		measured: []byte(`{"input_tokens":120,"input_tokens_details":{"cached_tokens":20},"output_tokens":30,"output_tokens_details":{"reasoning_tokens":10},"total_tokens":150}`),
		allocation: Allocation{
			ID: "allocation", TenantID: "tenant", SessionID: "session", EnvironmentID: "environment",
			ProviderKey: "provider", ComputePhase: "running", ComputeState: []byte(`{"current":{"name":"sandbox"}}`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := r.Resolve(t.Context(), "tenant", "session")
	if err != nil {
		t.Fatal(err)
	}
	if target.TenantID != "tenant" || target.SessionID != "session" || target.EnvironmentID != "environment" || target.Mode != v1.RuntimeModeManaged || target.Instance.AllocationID != "allocation" || target.Instance.ProviderKey != "provider" {
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

func TestObservationResolverRejectsInvalidCanonicalSessionUsage(t *testing.T) {
	for _, usage := range []string{
		`{"input_tokens":2,"input_tokens_details":{"cached_tokens":0},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":4}`,
		`{}`,
		`{"total_tokens":0}`,
		`{"input_tokens":0,"input_tokens_details":{"cached_tokens":-1},"output_tokens":0,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":0}`,
		`{"input_tokens":0,"input_tokens_details":{"cached_tokens":0},"output_tokens":0,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":0,"unknown":0}`,
	} {
		resolver, err := newTestResolver(t, resolverStore{session: sessions.Session{
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

func TestObservationResolverKeepsNullCanonicalSessionUsageAbsent(t *testing.T) {
	resolver, err := newTestResolver(t, resolverStore{session: sessions.Session{
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
func TestObservationResolverUsesMeasuredRatherThanPublicSessionUsage(t *testing.T) {
	measured := `{"input_tokens":7,"input_tokens_details":{"cached_tokens":0},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":10}`
	for _, test := range []struct {
		public, measured string
		want             *runtimeobs.TokenUsage
	}{
		{"null", measured, &runtimeobs.TokenUsage{InputTokens: 7, OutputTokens: 3}},
		{measured, "null", nil},
	} {
		resolver, err := newTestResolver(t, resolverStore{session: sessions.Session{
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

func TestObservationResolverKeepsUnsupportedModesDistinct(t *testing.T) {
	for _, tc := range []struct {
		mode        string
		environment *sessions.Environment
	}{
		{mode: "none"},
		{mode: "self_hosted", environment: &sessions.Environment{ID: "environment", TenantID: "tenant", SessionID: "session"}},
	} {
		r, err := newTestResolver(t, resolverStore{session: sessions.Session{ID: "session", TenantID: "tenant", Configuration: []byte(`{"environment":{"type":"` + tc.mode + `"}}`), Environment: tc.environment}})
		if err != nil {
			t.Fatal(err)
		}
		target, err := r.Resolve(t.Context(), "tenant", "session")
		if err != nil || string(target.Mode) != tc.mode {
			t.Fatalf("mode %s was not resolved accurately: %+v %v", tc.mode, target, err)
		}
	}
}

func TestObservationResolverReportsManagedAllocationAsUnavailable(t *testing.T) {
	r, err := newTestResolver(t, resolverStore{
		session:       sessions.Session{ID: "session", TenantID: "tenant", Configuration: []byte(`{"environment":{"type":"openai_hosted"}}`), Environment: &sessions.Environment{ID: "environment", TenantID: "tenant", SessionID: "session"}},
		allocationErr: ErrNotFound,
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := r.Resolve(t.Context(), "tenant", "session")
	if !errors.Is(err, runtimeobs.ErrUnavailable) || target.EnvironmentID != "environment" || target.Mode != v1.RuntimeModeManaged {
		t.Fatalf("allocation absence was not preserved: %+v %v", target, err)
	}
}

func TestObservationResolverRejectsMismatchedEnvironmentOwnership(t *testing.T) {
	for _, mode := range []string{"self_hosted", "openai_hosted"} {
		for _, environment := range []sessions.Environment{
			{ID: "environment", TenantID: "other", SessionID: "session"},
			{ID: "environment", TenantID: "tenant", SessionID: "other"},
		} {
			resolver, err := newTestResolver(t, resolverStore{session: sessions.Session{
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

func TestObservationResolverRejectsMismatchedAllocationOwnership(t *testing.T) {
	base := Allocation{
		ID: "allocation", TenantID: "tenant", SessionID: "session", EnvironmentID: "environment",
		ProviderKey: "provider",
	}
	for _, mutate := range []func(*Allocation){
		func(value *Allocation) { value.TenantID = "other" },
		func(value *Allocation) { value.SessionID = "other" },
		func(value *Allocation) { value.EnvironmentID = "other" },
	} {
		allocation := base
		mutate(&allocation)
		resolver, err := newTestResolver(t, resolverStore{
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
