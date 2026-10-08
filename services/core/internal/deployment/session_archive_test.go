package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestCheckArchiveDeployment(t *testing.T) {
	managed := Record{InstallationID: "installation", Provider: "docker", Generation: 4}
	for name, test := range map[string]struct {
		change func(*Record)
		want   error
	}{
		"current":          {func(*Record) {}, nil},
		"stale generation": {func(d *Record) { d.Generation = 5; d.InstallationID = "" }, &GenerationStaleError{CurrentGeneration: 5}},
		"no installation":  {func(d *Record) { d.InstallationID = "" }, ErrConflict},
		"no provider":      {func(d *Record) { d.Provider = "" }, ErrNotConfigured},
	} {
		d := managed
		test.change(&d)
		err := checkArchiveDeployment(d, 4)
		var stale *GenerationStaleError
		switch want := test.want.(type) {
		case nil:
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
		case *GenerationStaleError:
			if !errors.As(err, &stale) || stale.CurrentGeneration != want.CurrentGeneration {
				t.Errorf("%s: got %v, want stale generation %d", name, err, want.CurrentGeneration)
			}
		default:
			if !errors.Is(err, want) || errors.As(err, &stale) {
				t.Errorf("%s: got %v, want %v", name, err, want)
			}
		}
	}
}

func TestCheckArchiveReset(t *testing.T) {
	requested := time.Unix(100, 0)
	for name, test := range map[string]struct {
		reset *ResetState
		want  error
	}{
		"running reset":   {&ResetState{Clear: string(ResetAuto), RequestedAt: requested}, nil},
		"no reset":        {nil, ErrConflict},
		"another request": {&ResetState{Clear: string(ResetAuto), RequestedAt: requested.Add(time.Second)}, ErrConflict},
	} {
		if err := checkArchiveReset(Record{Reset: test.reset}, requested); !errors.Is(err, test.want) {
			t.Errorf("%s: got %v, want %v", name, err, test.want)
		}
	}
}

// fakeArchiveTx is a Session archive over fixed facts. Each method records
// its call; a test asserts the exact call log. The Session's Turn and input
// reads fail the test unless it settles the Session with idle.
type fakeArchiveTx struct {
	t                    testing.TB
	calls                []string
	deployment           Record
	environment          *sessions.Environment
	busy                 bool
	allocation           *Allocation
	loadActiveTurn       func() (sessions.Turn, bool, error)
	cancelPendingInput   func() error
	loadEnvironmentInput func() (*sessions.EnvironmentInputState, error)
	// audited is the administrator source the audit was recorded with.
	audited *adminaudit.Source
}

// idle settles a Session with no active Turn, pending input or input
// activity change.
func (f *fakeArchiveTx) idle() {
	f.loadActiveTurn = func() (sessions.Turn, bool, error) { return sessions.Turn{}, false, nil }
	f.cancelPendingInput = func() error { return nil }
	f.loadEnvironmentInput = func() (*sessions.EnvironmentInputState, error) { return nil, nil }
}

func (f *fakeArchiveTx) record(call string) { f.calls = append(f.calls, call) }

func (f *fakeArchiveTx) LoadDeployment() (Record, error) {
	f.record("LoadDeployment")
	return f.deployment, nil
}

func (f *fakeArchiveTx) LoadEnvironment(context.Context) (sessions.Environment, error) {
	f.record("LoadEnvironment")
	if f.environment == nil {
		return sessions.Environment{}, sessions.ErrNotFound
	}
	return *f.environment, nil
}

func (f *fakeArchiveTx) ExpireEnvironment(_ context.Context, environment string) error {
	f.record("ExpireEnvironment " + environment)
	return nil
}

func (f *fakeArchiveTx) LoadResetBusy() (bool, error) {
	f.record("LoadResetBusy")
	return f.busy, nil
}

func (f *fakeArchiveTx) LoadResetSource() (adminaudit.Source, error) {
	f.record("LoadResetSource")
	return adminaudit.Source{CredentialID: "admin"}, nil
}

func (f *fakeArchiveTx) LoadProject() (string, error) {
	f.record("LoadProject")
	return "project", nil
}

func (f *fakeArchiveTx) FindAllocation(environment string) (Allocation, bool, error) {
	f.record("FindAllocation " + environment)
	if f.allocation == nil {
		return Allocation{}, false, nil
	}
	return *f.allocation, true, nil
}

func (f *fakeArchiveTx) RequestArchiveCleanup(current Allocation) error {
	f.record("RequestArchiveCleanup " + current.DeviceID + " " + current.ID)
	return nil
}

func (f *fakeArchiveTx) ReleasePlacement() error {
	f.record("ReleasePlacement")
	return nil
}

func (f *fakeArchiveTx) RecordArchiveAudit(ctx context.Context) error {
	f.record("RecordArchiveAudit")
	if source, ok := adminaudit.FromContext(ctx); ok {
		f.audited = &source
	}
	return nil
}

func (f *fakeArchiveTx) LoadArchive(context.Context) (sessions.ManagedArchive, error) {
	f.record("LoadArchive")
	return sessions.ManagedArchive{SessionID: "session", EnvironmentID: "environment", State: "cleanup_pending"}, nil
}

func (f *fakeArchiveTx) LoadActiveTurn(context.Context) (sessions.Turn, bool, error) {
	f.record("LoadActiveTurn")
	if f.loadActiveTurn == nil {
		unexpected(f.t, "LoadActiveTurn")
	}
	return f.loadActiveTurn()
}

func (f *fakeArchiveTx) CancelPendingInput(context.Context) error {
	f.record("CancelPendingInput")
	if f.cancelPendingInput == nil {
		unexpected(f.t, "CancelPendingInput")
	}
	return f.cancelPendingInput()
}

func (f *fakeArchiveTx) LoadEnvironmentInput(context.Context) (*sessions.EnvironmentInputState, error) {
	f.record("LoadEnvironmentInput")
	if f.loadEnvironmentInput == nil {
		unexpected(f.t, "LoadEnvironmentInput")
	}
	return f.loadEnvironmentInput()
}

func (f *fakeArchiveTx) RequestTurnCancel(context.Context, string) error {
	unexpected(f.t, "RequestTurnCancel")
	return nil
}

func (f *fakeArchiveTx) LoadTurn(context.Context, string) (sessions.Turn, error) {
	unexpected(f.t, "LoadTurn")
	return sessions.Turn{}, nil
}

func (f *fakeArchiveTx) LoadEnding(context.Context, string) (sessions.Ending, error) {
	unexpected(f.t, "LoadEnding")
	return sessions.Ending{}, nil
}

func (f *fakeArchiveTx) ApplyTurnEnd(context.Context, string, sessions.TurnEnd) error {
	unexpected(f.t, "ApplyTurnEnd")
	return nil
}

func (f *fakeArchiveTx) LoadUsage(context.Context) (json.RawMessage, error) {
	unexpected(f.t, "LoadUsage")
	return nil, nil
}

func (f *fakeArchiveTx) AppendChanges(context.Context, ...sessions.SessionChange) error {
	unexpected(f.t, "AppendChanges")
	return nil
}

// archiveOperations builds execution operations whose Session archives run on
// tx with locked.
func archiveOperations(t *testing.T, tx *fakeArchiveTx, locked sessions.LockedSession) *ExecutionOperations {
	t.Helper()
	storage := &fakeExecutionStorage{t: t, withSessionArchive: func(ctx context.Context, tenantID, sessionID string, apply func(context.Context, sessions.LockedSession, SessionArchiveTx) error) error {
		if tenantID != "tenant" || sessionID != "session" {
			t.Fatalf("archived %s/%s", tenantID, sessionID)
		}
		return apply(ctx, locked, tx)
	}}
	operations, err := NewExecutionOperations(newService(t, &fakeStorage{t: t}, &fakeReader{t: t}, testPublicURL), storage)
	if err != nil {
		t.Fatal(err)
	}
	return operations
}

func TestArchiveSession(t *testing.T) {
	managed := Record{InstallationID: "installation", Provider: "docker", Generation: 1}
	hosted := &sessions.Environment{ID: "environment", Status: "connected", Configuration: json.RawMessage(`{"type":"openai_hosted"}`)}
	expired := &sessions.Environment{ID: "environment", Status: "expired", Configuration: hosted.Configuration}
	live := &Allocation{ID: "allocation", DeviceID: "device", ProviderKey: "installation", State: "running"}
	settle := []string{"LoadEnvironmentInput", "LoadActiveTurn", "CancelPendingInput", "LoadEnvironmentInput"}
	expire := append([]string{"LoadEnvironmentInput", "ExpireEnvironment environment"}, settle[1:]...)
	head := []string{"LoadDeployment", "LoadEnvironment", "FindAllocation environment"}
	join := func(parts ...[]string) []string {
		var calls []string
		for _, part := range parts {
			calls = append(calls, part...)
		}
		return calls
	}
	for _, test := range []struct {
		name        string
		locked      sessions.LockedSession
		environment *sessions.Environment
		allocation  *Allocation
		want        error
		calls       []string
	}{
		{"live allocation", sessions.LockedSession{}, hosted, live, nil,
			join(head, expire, []string{"RequestArchiveCleanup device allocation", "RecordArchiveAudit", "LoadArchive"})},
		{"no allocation", sessions.LockedSession{}, hosted, nil, nil, join(head, expire, []string{"ReleasePlacement", "RecordArchiveAudit", "LoadArchive"})},
		{"released allocation", sessions.LockedSession{}, hosted, &Allocation{ID: "allocation", ProviderKey: "previous", State: "released"}, nil,
			join(head, expire, []string{"RecordArchiveAudit", "LoadArchive"})},
		{"ended Environment", sessions.LockedSession{}, expired, live, nil,
			join(head, settle, []string{"RequestArchiveCleanup device allocation", "RecordArchiveAudit", "LoadArchive"})},
		{"allocation of another installation", sessions.LockedSession{}, hosted, &Allocation{ID: "allocation", ProviderKey: "previous", State: "running"}, ErrConflict, head},
		{"deleted Session", sessions.LockedSession{Deleted: true}, hosted, nil, sessions.ErrNotFound, nil},
		{"no Environment", sessions.LockedSession{}, nil, nil, sessions.ErrInvalidInput, head[:2]},
		{"self-hosted Environment", sessions.LockedSession{}, &sessions.Environment{ID: "environment", Configuration: json.RawMessage(`{"type":"self_hosted"}`)}, nil, sessions.ErrInvalidInput, head[:2]},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := &fakeArchiveTx{t: t, deployment: managed, environment: test.environment, allocation: test.allocation}
			if slices.Contains(test.calls, "LoadActiveTurn") {
				tx.idle()
			}
			result, err := archiveOperations(t, tx, test.locked).ArchiveSession(t.Context(), "tenant", "session", 1)
			if test.want == nil && (err != nil || result.State != "cleanup_pending") || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("got %v %v, want %v", result, err, test.want)
			}
			if strings.Join(tx.calls, "\n") != strings.Join(test.calls, "\n") {
				t.Fatalf("calls %q, want %q", tx.calls, test.calls)
			}
			if tx.audited != nil {
				t.Fatal("an administrator archive borrowed a reset source", tx.audited)
			}
		})
	}
}

func TestArchiveResetSession(t *testing.T) {
	requested := time.Unix(100, 0)
	resetting := func(clear ResetMode) Record {
		return Record{InstallationID: "installation", Provider: "docker", Generation: 1, Reset: &ResetState{Clear: string(clear), RequestedAt: requested}}
	}
	hosted := &sessions.Environment{ID: "environment", Status: "connected", Configuration: json.RawMessage(`{"type":"openai_hosted"}`)}
	failed := &sessions.Environment{ID: "environment", Status: "failed", Configuration: hosted.Configuration}
	for _, test := range []struct {
		name        string
		deployment  Record
		environment *sessions.Environment
		busy        bool
		requested   time.Time
		want        error
		calls       []string
	}{
		{"idle Session", resetting(ResetAuto), hosted, false, requested, nil,
			[]string{"LoadDeployment", "LoadEnvironment", "LoadResetBusy", "LoadResetSource", "LoadProject", "FindAllocation environment", "LoadEnvironmentInput", "ExpireEnvironment environment", "LoadActiveTurn", "CancelPendingInput", "LoadEnvironmentInput", "ReleasePlacement", "RecordArchiveAudit", "LoadArchive"}},
		{"busy Session", resetting(ResetAuto), hosted, true, requested, ErrSandboxResetSessionBusy, []string{"LoadDeployment", "LoadEnvironment", "LoadResetBusy"}},
		{"forced busy Session", resetting(ResetForce), hosted, true, requested, nil,
			[]string{"LoadDeployment", "LoadEnvironment", "LoadResetSource", "LoadProject", "FindAllocation environment", "LoadEnvironmentInput", "ExpireEnvironment environment", "LoadActiveTurn", "CancelPendingInput", "LoadEnvironmentInput", "ReleasePlacement", "RecordArchiveAudit", "LoadArchive"}},
		{"ended Environment", resetting(ResetAuto), failed, false, requested, nil, []string{"LoadDeployment", "LoadEnvironment", "LoadResetBusy", "LoadArchive"}},
		{"another reset", resetting(ResetAuto), hosted, false, requested.Add(time.Second), ErrConflict, []string{"LoadDeployment", "LoadEnvironment"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := &fakeArchiveTx{t: t, deployment: test.deployment, environment: test.environment, busy: test.busy}
			if slices.Contains(test.calls, "LoadActiveTurn") {
				tx.idle()
			}
			_, err := archiveOperations(t, tx, sessions.LockedSession{}).ArchiveResetSession(t.Context(), "tenant", "session", 1, test.requested)
			if test.want == nil && err != nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if strings.Join(tx.calls, "\n") != strings.Join(test.calls, "\n") {
				t.Fatalf("calls %q, want %q", tx.calls, test.calls)
			}
			if audited := tx.audited; test.want == nil && strings.Contains(strings.Join(test.calls, ","), "RecordArchiveAudit") && (audited == nil || audited.CredentialID != "admin" || audited.ProjectID != "project") {
				t.Fatal("reset archive was not audited with the reset source", audited)
			}
		})
	}
}
