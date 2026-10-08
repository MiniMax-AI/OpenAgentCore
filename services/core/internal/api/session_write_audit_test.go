package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmenttemplates"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type auditedSessionFixture struct {
	streamFixture
	err     error
	actions []string
	source  writeaudit.Source
}

func (f *auditedSessionFixture) FindSessionCreation(context.Context, string, string, json.RawMessage, identity.Subject) (sessions.Creation, error) {
	return sessions.Creation{Session: f.session}, nil
}

func (f *auditedSessionFixture) AuditSessionOperation(ctx context.Context, command sessions.AuditSessionOperationCommand) error {
	if command.TenantID != f.session.TenantID || command.SessionID != f.session.ID {
		return sessions.ErrNotFound
	}
	f.source, _ = writeaudit.FromContext(ctx)
	f.actions = append(f.actions, command.Action)
	return f.err
}

func TestSessionAuditOnlyRoutesFailClosed(t *testing.T) {
	for _, route := range []string{"empty-events", "creation-replay", "stream-replay"} {
		for _, fail := range []bool{false, true} {
			t.Run(route+map[bool]string{false: "/commit", true: "/rollback"}[fail], func(t *testing.T) {
				tenant, session := uuid.NewString(), uuid.NewString()
				f := &auditedSessionFixture{streamFixture: streamFixture{session: sessions.Session{ID: session, TenantID: tenant, Configuration: json.RawMessage(`{"environment":{"type":"none"},"agent":{"id":"agent","model":"test"}}`)}}}
				if fail {
					f.err = errors.New("audit unavailable")
				}
				deps, fakes := testDependencies(t)
				fakes.sessions.auditSessionOperation = f.AuditSessionOperation
				if route != "stream-replay" {
					fakes.sessionsReader.getSession = f.GetSession
				}
				if route != "empty-events" {
					fakes.sessionCreation.findSessionCreation = f.FindSessionCreation
				}
				h := &Handler{Dependencies: deps}
				ctx := context.WithValue(t.Context(), principalContextKey{}, identity.Principal{ProjectScope: identity.ProjectScope{TenantID: tenant}, SubjectKind: "service_account", SubjectID: "test"})
				ctx = writeaudit.WithSource(ctx, writeaudit.Source{TenantID: tenant, RequestID: "request", TraceID: "trace"})
				routeContext := chi.NewRouteContext()
				routeContext.URLParams.Add("session_id", session)
				ctx = context.WithValue(ctx, chi.RouteCtxKey, routeContext)
				body := map[string]string{
					"empty-events":    `{"events":[]}`,
					"creation-replay": `{"agent":{"model":"test"},"environment":{"type":"none"},"input":"hello"}`,
					"stream-replay":   `{"agent":{"model":"test"},"environment":{"type":"none"},"input":"hello","stream":true}`,
				}[route]
				r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(body)).WithContext(ctx)
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				if route == "empty-events" {
					h.createEvents(w, r)
				} else {
					h.createSession(w, r)
				}
				wantStatus, wantAction := 201, "create"
				if route == "empty-events" {
					wantStatus, wantAction = 202, "send_events"
				}
				if route == "stream-replay" {
					wantStatus = 201
				}
				if fail {
					wantStatus = 500
				}
				if w.Code != wantStatus || len(f.actions) != 1 || f.actions[0] != wantAction || f.source.RequestID != "request" {
					t.Fatal(w.Code, w.Body.String(), f.actions, f.source)
				}
			})
		}
	}
}

// A retry whose Template is gone answers with the Session that a concurrent
// request with the same key and intent committed after the entry lookup.
func TestSessionCreationReplaysAfterResolutionFails(t *testing.T) {
	session, lookups, audits := uuid.NewString(), 0, 0
	h, _, _ := testHandler(t, func(_ *Dependencies, f *testFakes) {
		f.environmentTemplatesReader.resolve = func(context.Context, string, string) (environmenttemplates.Resolved, error) {
			return environmenttemplates.Resolved{}, environmenttemplates.ErrNotFound
		}
		f.sessionCreation.findSessionCreation = func(context.Context, string, string, json.RawMessage, identity.Subject) (sessions.Creation, error) {
			if lookups++; lookups == 1 {
				return sessions.Creation{}, sessions.ErrNotFound
			}
			return sessions.Creation{Session: sessions.Session{ID: session}}, nil
		}
		f.sessions.auditSessionOperation = func(context.Context, sessions.AuditSessionOperationCommand) error {
			audits++
			return nil
		}
		f.sessionsReader.getSession = func(_ context.Context, tenant, id string) (sessions.Session, error) {
			return sessions.Session{ID: id, TenantID: tenant, Configuration: json.RawMessage(`{"environment":{"type":"none"},"agent":{"id":"agent","model":"test"}}`)}, nil
		}
	})
	r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted","environment_template_id":"saved"},"input":"Start."}`))
	r.Header.Set("Authorization", "Bearer test-api-key")
	r.Header.Set("OpenAI-Beta", "agents=v1")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", "retry")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), session) || lookups != 2 || audits != 1 {
		t.Fatal(w.Code, w.Body.String(), lookups, audits)
	}
}
