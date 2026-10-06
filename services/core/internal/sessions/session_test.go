package sessions

import (
	"errors"
	"strings"
	"testing"
)

func TestSettled(t *testing.T) {
	for name, test := range map[string]struct {
		active bool
		input  *EnvironmentInputState
		want   bool
	}{
		"idle":                      {false, nil, true},
		"active Turn":               {true, nil, false},
		"pending input":             {false, &EnvironmentInputState{State: EnvironmentInputPending}, false},
		"pending hosted initial":    {false, &EnvironmentInputState{State: EnvironmentInputPending, Initial: true, EnvironmentType: "openai_hosted"}, false},
		"failed input":              {false, &EnvironmentInputState{State: EnvironmentInputFailed}, true},
		"expired input":             {false, &EnvironmentInputState{State: EnvironmentInputExpired}, true},
		"active Turn without input": {true, &EnvironmentInputState{State: EnvironmentInputFailed}, false},
	} {
		if got := settled(test.active, test.input); got != test.want {
			t.Errorf("%s: settled = %v, want %v", name, got, test.want)
		}
	}
}

func TestDeleteSession(t *testing.T) {
	errStorage := errors.New("storage")
	pending := &EnvironmentInputState{State: EnvironmentInputPending}
	for _, test := range []struct {
		name   string
		locked LockedSession
		tx     fakeTx
		want   error
		calls  []string
	}{
		{"settled", LockedSession{}, fakeTx{loadActiveTurn: activeTurn(nil), loadEnvironmentInput: returns[*EnvironmentInputState](nil), applyDeletion: done, recordDeletionAudit: done}, nil,
			[]string{"LoadActiveTurn", "LoadEnvironmentInput", "ApplyDeletion", "RecordDeletionAudit"}},
		{"repeated deletion only audits", LockedSession{Deleted: true}, fakeTx{recordDeletionAudit: done}, nil, []string{"RecordDeletionAudit"}},
		{"active Turn", LockedSession{}, fakeTx{loadActiveTurn: activeTurn(&Turn{ID: "turn"})}, ErrNotIdle, []string{"LoadActiveTurn"}},
		{"pending input", LockedSession{}, fakeTx{loadActiveTurn: activeTurn(nil), loadEnvironmentInput: returns(pending)}, ErrNotIdle, []string{"LoadActiveTurn", "LoadEnvironmentInput"}},
		{"failed deletion", LockedSession{}, fakeTx{loadActiveTurn: activeTurn(nil), loadEnvironmentInput: returns[*EnvironmentInputState](nil), applyDeletion: func() error { return errStorage }}, errStorage,
			[]string{"LoadActiveTurn", "LoadEnvironmentInput", "ApplyDeletion"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := test.tx
			tx.t = t
			storage := &fakeStorage{t: t, deletion: &tx, locked: test.locked}
			service, err := NewService(storage, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = service.DeleteSession(t.Context(), DeleteSessionCommand{TenantID: "tenant", SessionID: "session"})
			if test.want == nil && err != nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if got := strings.Join(storage.calls, "\n"); got != "WithSessionDeletion tenant session" {
				t.Fatalf("storage calls %q", got)
			}
			if strings.Join(tx.calls, "\n") != strings.Join(test.calls, "\n") {
				t.Fatalf("calls %q, want %q", tx.calls, test.calls)
			}
		})
	}
}

func TestUpdateSessionMetadata(t *testing.T) {
	storage := &fakeStorage{t: t, updateMetadata: func(string) (Session, error) { return Session{ID: "session"}, nil }}
	service, err := NewService(storage, nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.UpdateSessionMetadata(t.Context(), UpdateSessionMetadataCommand{TenantID: "tenant", SessionID: "session", Metadata: map[string]string{"k": "v"}})
	if err != nil || session.ID != "session" || strings.Join(storage.calls, "\n") != `UpdateSessionMetadata tenant session {"k":"v"}` {
		t.Fatal(session, err, storage.calls)
	}
	invalid := map[string]string{"k": strings.Repeat("v", 64*1024)}
	if _, err := service.UpdateSessionMetadata(t.Context(), UpdateSessionMetadataCommand{TenantID: "tenant", SessionID: "session", Metadata: invalid}); !errors.Is(err, ErrInvalidInput) || len(storage.calls) != 1 {
		t.Fatal("invalid metadata reached storage", err, storage.calls)
	}
}

func TestAuditSessionOperation(t *testing.T) {
	storage := &fakeStorage{t: t, auditOperation: done}
	service, err := NewService(storage, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"create", "send_events"} {
		if err := service.AuditSessionOperation(t.Context(), AuditSessionOperationCommand{TenantID: "tenant", SessionID: "session", Action: action}); err != nil {
			t.Fatal(action, err)
		}
	}
	if err := service.AuditSessionOperation(t.Context(), AuditSessionOperationCommand{TenantID: "tenant", SessionID: "session", Action: "delete"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("unsupported action", err)
	}
	if got := strings.Join(storage.calls, "\n"); got != "AuditSessionOperation tenant session create\nAuditSessionOperation tenant session send_events" {
		t.Fatalf("calls %q", got)
	}
}
