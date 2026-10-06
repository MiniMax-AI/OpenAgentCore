package sessions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func (f *fakeTx) LoadSessionDevice(context.Context) (ExecutionDevice, bool, error) {
	f.record("LoadSessionDevice", f.loadSessionDevice != nil)
	return f.loadSessionDevice()
}

func (f *fakeTx) ClaimInitialization(_ context.Context, environment string) (bool, error) {
	f.record("ClaimInitialization", f.claimInitialization != nil, environment)
	return f.claimInitialization()
}

func (f *fakeTx) CompleteInitialization(_ context.Context, environment string) (bool, error) {
	f.record("CompleteInitialization", f.completeInitialization != nil, environment)
	return f.completeInitialization()
}

func (f *fakeTx) FailInitialization(_ context.Context, environment string) error {
	f.record("FailInitialization", f.failInitialization != nil, environment)
	return f.failInitialization()
}

func (f *fakeTx) LoadConnection(_ context.Context, environment string) (EnvironmentConnection, bool, error) {
	f.record("LoadConnection", f.loadConnection != nil, environment)
	return f.loadConnection()
}

func (f *fakeTx) ReplaceConnection(_ context.Context, environment, generation string) error {
	f.record("ReplaceConnection", f.replaceConnection != nil, environment, generation)
	return f.replaceConnection()
}

func (f *fakeTx) AdvanceConnection(_ context.Context, environment string, revision int64) error {
	f.record("AdvanceConnection", f.advanceConnection != nil, environment, fmt.Sprint(revision))
	return f.advanceConnection()
}

func (f *fakeTx) DeleteConnection(_ context.Context, environment string) error {
	f.record("DeleteConnection", f.deleteConnection != nil, environment)
	return f.deleteConnection()
}

func (f *fakeTx) SetConnectionStatus(_ context.Context, environment, status string) error {
	f.record("SetConnectionStatus", f.setConnectionStatus != nil, environment, status)
	return f.setConnectionStatus()
}

func (f *fakeTx) LoadDevice(_ context.Context, device string) (bool, error) {
	f.record("LoadDevice", f.loadDevice != nil, device)
	return f.loadDevice()
}

func (f *fakeTx) BindDevice(_ context.Context, device string) error {
	f.record("BindDevice", f.bindDevice != nil, device)
	return f.bindDevice()
}

func (f *fakeTx) AuthorizeEnrollment(context.Context) (EnrollmentAuthority, error) {
	f.record("AuthorizeEnrollment", f.authorizeEnrollment != nil)
	return f.authorizeEnrollment()
}

func (f *fakeTx) EnrollDevice(_ context.Context, key string) (string, error) {
	f.record("EnrollDevice", f.enrollDevice != nil, key)
	return f.enrollDevice()
}

// environmentTx is the transaction the Environment family's With methods
// apply in; a call without one fails the test.
func (s *fakeExecutionStorage) environmentTx(name string) *fakeTx {
	s.t.Helper()
	if s.tx == nil {
		s.t.Fatalf("unexpected call to %s", name)
	}
	return s.tx
}

func (s *fakeExecutionStorage) WithInitialization(ctx context.Context, tenant, session string, apply func(context.Context, InitializationTx, LockedSession) error) error {
	s.environmentTx("WithInitialization").record("WithInitialization", true, tenant, session)
	return apply(ctx, s.tx, s.locked)
}

func (s *fakeExecutionStorage) WithConnection(ctx context.Context, tenant, environment string, apply func(context.Context, ConnectionTx, LockedSession) error) error {
	s.environmentTx("WithConnection").record("WithConnection", true, tenant, environment)
	if err := s.connections[environment]; err != nil {
		return err
	}
	return apply(ctx, s.tx, s.locked)
}

func (s *fakeExecutionStorage) WithDeviceBinding(ctx context.Context, tenant, session string, apply func(context.Context, DeviceBindingTx, LockedSession) error) error {
	s.environmentTx("WithDeviceBinding").record("WithDeviceBinding", true, tenant, session)
	return apply(ctx, s.tx, s.locked)
}

func (s *fakeExecutionStorage) ListEnvironmentConnections(_ context.Context, after string) ([]EnvironmentKey, error) {
	s.environmentTx("ListEnvironmentConnections").record("ListEnvironmentConnections", s.pages != nil, after)
	var page []EnvironmentKey
	if len(s.pages) > 0 {
		page, s.pages = s.pages[0], s.pages[1:]
	}
	return page, nil
}

// loads is a fake method that reads value and reports whether it was found.
func loads[T any](value T, found bool) func() (T, bool, error) {
	return func() (T, bool, error) { return value, found, nil }
}

func executionOperations(t *testing.T, storage *fakeExecutionStorage) *ExecutionOperations {
	t.Helper()
	storage.t = t
	operations, err := NewExecutionOperations(storage)
	if err != nil {
		t.Fatal(err)
	}
	return operations
}

var hostedEnvironment = Environment{ID: "environment", SessionID: "session", Status: "pending", Configuration: []byte(`{"type":"openai_hosted"}`)}

func TestInitializationOwnerAndDeviceRules(t *testing.T) {
	owner := EnvironmentInitialization{EnvironmentID: "environment", DeviceID: "device"}
	for _, test := range []struct {
		name        string
		environment Environment
		want        error
	}{
		{"live", Environment{ID: "environment", Status: "pending"}, nil},
		{"another Environment", Environment{ID: "other", Status: "pending"}, ErrDeviceBindingConflict},
		{"failed", Environment{ID: "environment", Status: "failed"}, ErrNotFound},
		{"expired", Environment{ID: "environment", Status: "expired"}, ErrNotFound},
	} {
		if err := checkInitializationOwner(owner, test.environment); !errors.Is(err, test.want) {
			t.Fatalf("owner %s: %v", test.name, err)
		}
	}
	environment := Environment{ID: "environment"}
	for _, test := range []struct {
		name  string
		bound ExecutionDevice
		found bool
		want  error
	}{
		{"listed device", ExecutionDevice{ID: "device", EnvironmentID: "environment"}, true, nil},
		{"no device", ExecutionDevice{}, false, ErrNotFound},
		{"another device", ExecutionDevice{ID: "other", EnvironmentID: "environment"}, true, ErrTurnConflict},
		{"another Environment's device", ExecutionDevice{ID: "device", EnvironmentID: "other"}, true, ErrTurnConflict},
	} {
		if err := checkInitializationDevice(owner, environment, test.bound, test.found); !errors.Is(err, test.want) {
			t.Fatalf("device %s: %v", test.name, err)
		}
	}
}

func TestInitializationTransitionsRunUnderTheListedDevice(t *testing.T) {
	owner := EnvironmentInitialization{TenantID: "tenant", SessionID: "session", EnvironmentID: "environment", DeviceID: "device"}
	listed := func() (ExecutionDevice, bool, error) {
		return ExecutionDevice{ID: "device", EnvironmentID: "environment"}, true, nil
	}
	checked := []string{"WithInitialization tenant session", "LoadEnvironment", "LoadSessionDevice"}
	for _, test := range []struct {
		name    string
		tx      fakeTx
		locked  LockedSession
		operate func(*ExecutionOperations) error
		want    error
		calls   []string
	}{
		{"claim", fakeTx{loadEnvironment: returns(hostedEnvironment), loadSessionDevice: listed, claimInitialization: returns(true)},
			LockedSession{}, func(o *ExecutionOperations) error { return o.ClaimEnvironmentInitialization(t.Context(), owner) },
			nil, append(checked, "ClaimInitialization environment")},
		{"claim that did not apply", fakeTx{loadEnvironment: returns(hostedEnvironment), loadSessionDevice: listed, claimInitialization: returns(false)},
			LockedSession{}, func(o *ExecutionOperations) error { return o.ClaimEnvironmentInitialization(t.Context(), owner) },
			ErrTurnConflict, append(checked, "ClaimInitialization environment")},
		{"complete", fakeTx{loadEnvironment: returns(hostedEnvironment), loadSessionDevice: listed, completeInitialization: returns(true)},
			LockedSession{}, func(o *ExecutionOperations) error { return o.CompleteEnvironmentInitialization(t.Context(), owner) },
			nil, append(checked, "CompleteInitialization environment")},
		{"device moved", fakeTx{loadEnvironment: returns(hostedEnvironment), loadSessionDevice: loads(ExecutionDevice{ID: "other", EnvironmentID: "environment"}, true)},
			LockedSession{}, func(o *ExecutionOperations) error { return o.CompleteEnvironmentInitialization(t.Context(), owner) },
			ErrTurnConflict, checked},
		{"deleted Session", fakeTx{},
			LockedSession{Deleted: true}, func(o *ExecutionOperations) error { return o.ClaimEnvironmentInitialization(t.Context(), owner) },
			ErrNotFound, checked[:1]},
		{"failed Environment", fakeTx{loadEnvironment: returns(Environment{ID: "environment", Status: "failed"})},
			LockedSession{}, func(o *ExecutionOperations) error { return o.ClaimEnvironmentInitialization(t.Context(), owner) },
			ErrNotFound, checked[:2]},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := test.tx
			tx.t = t
			err := test.operate(executionOperations(t, &fakeExecutionStorage{tx: &tx, locked: test.locked}))
			if test.want == nil && err != nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			assertCalls(t, &tx, test.calls...)
		})
	}
}

func TestFailEnvironmentInitializationKeepsACompletedPreparation(t *testing.T) {
	owner := EnvironmentInitialization{TenantID: "tenant", SessionID: "session", EnvironmentID: "environment"}
	failure := ProvisioningFailure{Step: ProvisioningHarness}

	complete := hostedEnvironment
	complete.Initialization = "complete"
	tx := fakeTx{t: t, loadEnvironment: returns(complete)}
	err := executionOperations(t, &fakeExecutionStorage{tx: &tx}).FailEnvironmentInitialization(t.Context(), owner, failure)
	if !errors.Is(err, ErrTurnConflict) {
		t.Fatalf("completed preparation failed: %v", err)
	}
	assertCalls(t, &tx, "WithInitialization tenant session", "LoadEnvironment")

	// The preparation fails before the Environment does; the device is not
	// consulted.
	tx = fakeTx{t: t, loadEnvironment: returns(hostedEnvironment), failInitialization: done,
		recordEnvironmentFailure: func() (time.Time, error) { return time.Time{}, errStorage }}
	err = executionOperations(t, &fakeExecutionStorage{tx: &tx}).FailEnvironmentInitialization(t.Context(), owner, failure)
	if !errors.Is(err, errStorage) {
		t.Fatal(err)
	}
	assertCalls(t, &tx, "WithInitialization tenant session", "LoadEnvironment", "FailInitialization environment",
		"RecordEnvironmentFailure environment "+failure.Reason())
}

// quietInput is a fake LoadEnvironmentInput with no input activity.
func quietInput() (*EnvironmentInputState, error) { return nil, nil }

func TestConnectionGenerationsAreOrdered(t *testing.T) {
	const generation = "0b7c6f0e-3d7b-4bd2-9f55-6f1d3f0f8a11"
	connected := hostedEnvironment
	connected.Status = "connected"
	var changes []SessionChange
	tracked := func(calls ...string) []string {
		return append(append([]string{"WithConnection tenant environment", "LoadEnvironment", "LoadEnvironmentInput"}, calls...), "LoadEnvironmentInput")
	}
	for _, test := range []struct {
		name    string
		tx      fakeTx
		locked  LockedSession
		operate func(*ExecutionOperations) error
		want    error
		calls   []string
	}{
		{"replacement disconnects", fakeTx{loadEnvironment: returns(connected), loadEnvironmentInput: quietInput, loadConnection: loads(EnvironmentConnection{}, false), replaceConnection: done, setConnectionStatus: done, appendChanges: collect(&changes)}, LockedSession{},
			func(o *ExecutionOperations) error {
				return o.ReplaceEnvironmentConnection(t.Context(), "tenant", "environment", strings.ToUpper(generation))
			},
			nil, tracked("LoadConnection environment", "ReplaceConnection environment "+generation, "SetConnectionStatus environment disconnected", "AppendChanges agent.session.environment.disconnected")},
		{"repeated replacement", fakeTx{loadEnvironment: returns(connected), loadEnvironmentInput: quietInput, loadConnection: loads(EnvironmentConnection{Generation: generation}, true)}, LockedSession{},
			func(o *ExecutionOperations) error {
				return o.ReplaceEnvironmentConnection(t.Context(), "tenant", "environment", generation)
			},
			nil, tracked("LoadConnection environment")},
		{"replacement of a failed Environment", fakeTx{loadEnvironment: returns(Environment{ID: "environment", Status: "failed"}), loadEnvironmentInput: quietInput}, LockedSession{},
			func(o *ExecutionOperations) error {
				return o.ReplaceEnvironmentConnection(t.Context(), "tenant", "environment", generation)
			},
			ErrInvalidInput, tracked()[:3]},
		{"newer observation connects", fakeTx{loadEnvironment: returns(hostedEnvironment), loadEnvironmentInput: quietInput, loadConnection: loads(EnvironmentConnection{Generation: generation, Revision: 1}, true), advanceConnection: done, setConnectionStatus: done, appendChanges: collect(&changes)}, LockedSession{},
			func(o *ExecutionOperations) error {
				return o.ObserveEnvironmentConnection(t.Context(), "tenant", "environment", generation, 2, true)
			},
			nil, tracked("LoadConnection environment", "AdvanceConnection environment 2", "SetConnectionStatus environment connected", "AppendChanges agent.session.environment.connected")},
		{"observation without a status change", fakeTx{loadEnvironment: returns(connected), loadEnvironmentInput: quietInput, loadConnection: loads(EnvironmentConnection{Generation: generation, Revision: 1}, true), advanceConnection: done}, LockedSession{},
			func(o *ExecutionOperations) error {
				return o.ObserveEnvironmentConnection(t.Context(), "tenant", "environment", generation, 2, true)
			},
			nil, tracked("LoadConnection environment", "AdvanceConnection environment 2")},
		{"stale observation", fakeTx{loadEnvironment: returns(hostedEnvironment), loadEnvironmentInput: quietInput, loadConnection: loads(EnvironmentConnection{Generation: generation, Revision: 2}, true)}, LockedSession{},
			func(o *ExecutionOperations) error {
				return o.ObserveEnvironmentConnection(t.Context(), "tenant", "environment", generation, 2, true)
			},
			nil, tracked("LoadConnection environment")},
		{"observation of a replaced generation", fakeTx{loadEnvironment: returns(hostedEnvironment), loadEnvironmentInput: quietInput, loadConnection: loads(EnvironmentConnection{Generation: "d2b0c1a4-4c83-4b7f-8f0a-3c9b54bb1d52", Revision: 1}, true)}, LockedSession{},
			func(o *ExecutionOperations) error {
				return o.ObserveEnvironmentConnection(t.Context(), "tenant", "environment", generation, 5, false)
			},
			nil, tracked("LoadConnection environment")},
		{"observation of a failed Environment", fakeTx{loadEnvironment: returns(Environment{ID: "environment", Status: "failed"}), loadEnvironmentInput: quietInput, loadConnection: loads(EnvironmentConnection{Generation: generation, Revision: 1}, true)}, LockedSession{},
			func(o *ExecutionOperations) error {
				return o.ObserveEnvironmentConnection(t.Context(), "tenant", "environment", generation, 2, true)
			},
			ErrInvalidInput, tracked("LoadConnection environment")[:4]},
		{"deleted Session", fakeTx{}, LockedSession{Deleted: true},
			func(o *ExecutionOperations) error {
				return o.ObserveEnvironmentConnection(t.Context(), "tenant", "environment", generation, 2, true)
			},
			ErrNotFound, []string{"WithConnection tenant environment"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := test.tx
			tx.t = t
			changes = nil
			err := test.operate(executionOperations(t, &fakeExecutionStorage{tx: &tx, locked: test.locked}))
			if test.want == nil && err != nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			assertCalls(t, &tx, test.calls...)
			for _, change := range changes {
				if state := change.Event.Environment; state == nil || state.ID != "environment" || state.Type != "openai_hosted" {
					t.Fatalf("change %+v", change.Event)
				}
			}
		})
	}
	for _, invalid := range []struct {
		generation string
		revision   int64
	}{{"not-a-uuid", 1}, {"00000000-0000-0000-0000-000000000000", 1}, {generation, 0}} {
		tx := fakeTx{t: t}
		operations := executionOperations(t, &fakeExecutionStorage{tx: &tx})
		if err := operations.ObserveEnvironmentConnection(t.Context(), "tenant", "environment", invalid.generation, invalid.revision, true); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("observation %+v: %v", invalid, err)
		}
		if invalid.revision > 0 {
			if err := operations.ReplaceEnvironmentConnection(t.Context(), "tenant", "environment", invalid.generation); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("replacement %+v: %v", invalid, err)
			}
		}
		assertCalls(t, &tx)
	}
}

func TestReconcileEnvironmentConnectionsDisconnectsEveryPage(t *testing.T) {
	connected := hostedEnvironment
	connected.Status = "connected"
	var changes []SessionChange
	tx := fakeTx{t: t, loadEnvironment: returns(connected), loadEnvironmentInput: quietInput, deleteConnection: done, setConnectionStatus: done, appendChanges: collect(&changes)}
	storage := &fakeExecutionStorage{tx: &tx, pages: [][]EnvironmentKey{{{"tenant", "environment"}, {"tenant", "gone"}}, {{"tenant", "environment"}}}, connections: map[string]error{"gone": ErrNotFound}}
	if err := executionOperations(t, storage).ReconcileEnvironmentConnections(t.Context()); err != nil {
		t.Fatal(err)
	}
	reconcile := []string{"WithConnection tenant environment", "LoadEnvironment", "LoadEnvironmentInput", "DeleteConnection environment",
		"SetConnectionStatus environment disconnected", "AppendChanges agent.session.environment.disconnected", "LoadEnvironmentInput"}
	want := append([]string{"ListEnvironmentConnections "}, reconcile...)
	want = append(want, "WithConnection tenant gone", "ListEnvironmentConnections gone")
	want = append(want, reconcile...)
	want = append(want, "ListEnvironmentConnections environment")
	assertCalls(t, &tx, want...)

	tx = fakeTx{t: t}
	storage = &fakeExecutionStorage{tx: &tx, pages: [][]EnvironmentKey{{{"tenant", "environment"}}}, connections: map[string]error{"environment": errStorage}}
	if err := executionOperations(t, storage).ReconcileEnvironmentConnections(t.Context()); !errors.Is(err, errStorage) {
		t.Fatalf("storage failure: %v", err)
	}
}

func TestBindSessionDeviceRequiresTheTenantsDevice(t *testing.T) {
	// A malformed device is ErrInvalidInput before any storage call, whether or
	// not the Session exists.
	if err := executionOperations(t, &fakeExecutionStorage{}).BindSessionDevice(t.Context(), "tenant", "missing", "device"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("malformed device: %v", err)
	}

	device := "5b0cf2a4-6c41-4f55-9d2f-3f6f1b1c7e10"
	tx := fakeTx{t: t, loadDevice: returns(false)}
	operations := executionOperations(t, &fakeExecutionStorage{tx: &tx, locked: LockedSession{Deleted: true}})
	if err := operations.BindSessionDevice(t.Context(), "tenant", "session", device); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown device: %v", err)
	}
	assertCalls(t, &tx, "WithDeviceBinding tenant session", "LoadDevice "+device)

	// A publicly deleted Session still binds.
	tx = fakeTx{t: t, loadDevice: returns(true), bindDevice: func() error { return ErrDeviceBindingConflict }}
	operations = executionOperations(t, &fakeExecutionStorage{tx: &tx, locked: LockedSession{Deleted: true}})
	if err := operations.BindSessionDevice(t.Context(), "tenant", "session", device); !errors.Is(err, ErrDeviceBindingConflict) {
		t.Fatalf("conflict: %v", err)
	}
	assertCalls(t, &tx, "WithDeviceBinding tenant session", "LoadDevice "+device, "BindDevice "+device)
}
