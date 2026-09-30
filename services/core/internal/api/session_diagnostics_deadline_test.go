package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

type diagnosticDeadlineStore struct {
	diagnosticSnapshotStore
	observed context.Context
}

func (s *diagnosticDeadlineStore) GetSessionDiagnosticsSnapshot(ctx context.Context, tenant, session string) (store.Session, error) {
	s.observed = ctx
	return s.diagnosticSnapshotStore.GetSessionDiagnosticsSnapshot(ctx, tenant, session)
}
func (s *diagnosticDeadlineStore) GetTurnDiagnosticsSnapshot(ctx context.Context, tenant, session, turn string) (store.TurnDiagnosticsSnapshot, error) {
	s.observed = ctx
	return s.diagnosticSnapshotStore.GetTurnDiagnosticsSnapshot(ctx, tenant, session, turn)
}

func TestDiagnosticsReadDeadline(t *testing.T) {
	id, turn := uuid.NewString(), uuid.NewString()
	session := store.Session{ID: id, Configuration: json.RawMessage(`{"agent":{"id":"agent_root","model":"test"},"environment":{"type":"none"}}`), LastTurn: &store.Turn{ID: turn, SessionID: id, Status: store.TurnCompleted}}
	for _, suffix := range []string{"/diagnostics", "/turns/" + turn + "/diagnostics"} {
		for _, shorter := range []bool{false, true} {
			t.Run(suffix+map[bool]string{false: "/server", true: "/caller"}[shorter], func(t *testing.T) {
				source := &diagnosticDeadlineStore{diagnosticSnapshotStore: diagnosticSnapshotStore{session: session}}
				h, _, _ := adminTestHandler(t, serveDiagnostics(source))
				ctx := t.Context()
				var callerDeadline time.Time
				if shorter {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, time.Second)
					defer cancel()
					callerDeadline, _ = ctx.Deadline()
				}
				r := httptest.NewRequest(http.MethodGet, adminSessionsPath+id+suffix, nil).WithContext(ctx)
				r.Header.Set("Authorization", "Bearer admin")
				w := httptest.NewRecorder()
				before := time.Now()
				h.ServeHTTP(w, r)
				after := time.Now()
				if w.Code != http.StatusOK || source.observed == nil {
					t.Fatal(w.Code, w.Body)
				}
				deadline, ok := source.observed.Deadline()
				if !ok {
					t.Fatal("diagnostic snapshot has no deadline")
				}
				if shorter {
					if !deadline.Equal(callerDeadline) {
						t.Fatal("caller deadline changed", deadline, callerDeadline)
					}
				} else if deadline.Before(before.Add(5*time.Second)) || deadline.After(after.Add(5*time.Second)) {
					t.Fatal("diagnostic snapshot does not have a five-second budget", deadline)
				}
				if source.observed.Err() != context.Canceled {
					t.Fatal("snapshot context retained after response", source.observed.Err())
				}
			})
		}
	}
}
