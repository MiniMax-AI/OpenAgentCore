package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func (f *auditedSessionFixture) AuditSessionOperation(ctx context.Context, tenant, session, action string) error {
	if tenant != f.session.TenantID || session != f.session.ID {
		return sessions.ErrNotFound
	}
	f.source, _ = writeaudit.FromContext(ctx)
	f.actions = append(f.actions, action)
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
					fakes.sessions.getSession = f.GetSession
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
				r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(`{"events":[]}`)).WithContext(ctx)
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				if route == "empty-events" {
					h.createEvents(w, r)
				} else if !h.recoverSessionCreation(w, r, "key", json.RawMessage(`{}`), route == "stream-replay") {
					t.Fatal("replay not handled")
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
