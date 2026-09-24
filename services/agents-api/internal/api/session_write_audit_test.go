package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/writeaudit"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// General wire fixtures do not persist resources; dedicated audit fixtures below
// check the no-op/replay boundary independently of business Store auditing.
func (s *recordingStore) AuditSessionOperation(ctx context.Context, tenant, session, action string) error {
	if auditor, ok := s.ResourceStore.(sessionWriteAuditor); ok {
		return auditor.AuditSessionOperation(ctx, tenant, session, action)
	}
	return nil
}

func (f *streamFixture) AuditSessionOperation(context.Context, string, string, string) error {
	return nil
}

type auditedSessionFixture struct {
	streamFixture
	err     error
	actions []string
	source  writeaudit.Source
}

func (f *auditedSessionFixture) FindSessionCreation(context.Context, string, string, json.RawMessage, identity.Subject) (store.SessionCreation, error) {
	return store.SessionCreation{Session: f.session}, nil
}

func (f *auditedSessionFixture) AuditSessionOperation(ctx context.Context, tenant, session, action string) error {
	if tenant != f.session.TenantID || session != f.session.ID {
		return store.ErrNotFound
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
				f := &auditedSessionFixture{streamFixture: streamFixture{session: store.Session{ID: session, TenantID: tenant, Configuration: json.RawMessage(`{"environment":{"type":"none"},"agent":{"id":"agent","model":"test"}}`)}}}
				if fail {
					f.err = errors.New("audit unavailable")
				}
				h := &Handler{store: f}
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
