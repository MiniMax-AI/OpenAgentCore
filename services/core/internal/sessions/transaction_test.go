package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeTx is a strict Session transaction. Each method records its call, with
// the arguments that identify it, then runs its func; a method whose func is
// unset fails the test. Tests set only the funcs they expect and assert the
// exact call log.
type fakeTx struct {
	t     *testing.T
	calls []string

	loadUsage                func() (json.RawMessage, error)
	appendChanges            func([]SessionChange) error
	loadActiveTurn           func() (Turn, bool, error)
	requestTurnCancel        func() error
	loadTurn                 func() (Turn, error)
	loadEnding               func() (Ending, error)
	applyTurnEnd             func(TurnEnd) error
	cancelPendingInput       func() error
	failPendingInput         func() error
	loadEnvironmentInput     func() (*EnvironmentInputState, error)
	loadEnvironment          func() (Environment, error)
	recordEnvironmentFailure func() (time.Time, error)
	expireEnvironment        func() error
	loadComputeSuspension    func() (bool, error)
	loadPendingFileWrite     func() (bool, error)
	loadBoundDevice          func() (bool, error)
	insertEnvironmentDevice  func() error
}

var (
	_ EnvironmentTerminationTx = (*fakeTx)(nil)
	_ InputStartTx             = (*fakeTx)(nil)
	_ ComputeAdmissionTx       = (*fakeTx)(nil)
	_ EnvironmentDeviceTx      = (*fakeTx)(nil)
)

func (f *fakeTx) record(name string, set bool, detail ...string) {
	f.t.Helper()
	if !set {
		f.t.Fatalf("unexpected call to %s", name)
	}
	f.calls = append(f.calls, strings.Join(append([]string{name}, detail...), " "))
}

func (f *fakeTx) LoadUsage(context.Context) (json.RawMessage, error) {
	f.record("LoadUsage", f.loadUsage != nil)
	return f.loadUsage()
}

func (f *fakeTx) AppendChanges(_ context.Context, changes ...SessionChange) error {
	f.record("AppendChanges", f.appendChanges != nil, strings.Join(types(changes), ","))
	return f.appendChanges(changes)
}

func (f *fakeTx) LoadActiveTurn(context.Context) (Turn, bool, error) {
	f.record("LoadActiveTurn", f.loadActiveTurn != nil)
	return f.loadActiveTurn()
}

func (f *fakeTx) RequestTurnCancel(_ context.Context, turn string) error {
	f.record("RequestTurnCancel", f.requestTurnCancel != nil, turn)
	return f.requestTurnCancel()
}

func (f *fakeTx) LoadTurn(_ context.Context, turn string) (Turn, error) {
	f.record("LoadTurn", f.loadTurn != nil, turn)
	return f.loadTurn()
}

func (f *fakeTx) LoadEnding(_ context.Context, turn string) (Ending, error) {
	f.record("LoadEnding", f.loadEnding != nil, turn)
	return f.loadEnding()
}

func (f *fakeTx) ApplyTurnEnd(_ context.Context, turn string, end TurnEnd) error {
	f.record("ApplyTurnEnd", f.applyTurnEnd != nil, turn)
	return f.applyTurnEnd(end)
}

func (f *fakeTx) CancelPendingInput(context.Context) error {
	f.record("CancelPendingInput", f.cancelPendingInput != nil)
	return f.cancelPendingInput()
}

func (f *fakeTx) FailPendingInput(context.Context) error {
	f.record("FailPendingInput", f.failPendingInput != nil)
	return f.failPendingInput()
}

func (f *fakeTx) LoadEnvironmentInput(context.Context) (*EnvironmentInputState, error) {
	f.record("LoadEnvironmentInput", f.loadEnvironmentInput != nil)
	return f.loadEnvironmentInput()
}

func (f *fakeTx) LoadEnvironment(context.Context) (Environment, error) {
	f.record("LoadEnvironment", f.loadEnvironment != nil)
	return f.loadEnvironment()
}

func (f *fakeTx) RecordEnvironmentFailure(_ context.Context, environment, reason string, _ *ProvisioningFailureDetail) (time.Time, error) {
	f.record("RecordEnvironmentFailure", f.recordEnvironmentFailure != nil, environment, reason)
	return f.recordEnvironmentFailure()
}

func (f *fakeTx) ExpireEnvironment(_ context.Context, environment string) error {
	f.record("ExpireEnvironment", f.expireEnvironment != nil, environment)
	return f.expireEnvironment()
}

func (f *fakeTx) LoadComputeSuspension(context.Context) (bool, error) {
	f.record("LoadComputeSuspension", f.loadComputeSuspension != nil)
	return f.loadComputeSuspension()
}

func (f *fakeTx) LoadPendingFileWrite(context.Context) (bool, error) {
	f.record("LoadPendingFileWrite", f.loadPendingFileWrite != nil)
	return f.loadPendingFileWrite()
}

func (f *fakeTx) LoadBoundDevice(context.Context) (bool, error) {
	f.record("LoadBoundDevice", f.loadBoundDevice != nil)
	return f.loadBoundDevice()
}

func (f *fakeTx) InsertEnvironmentDevice(_ context.Context, device ExecutionDevice, credentialHash string) error {
	f.record("InsertEnvironmentDevice", f.insertEnvironmentDevice != nil, device.ID, device.Name, device.EnvironmentID, credentialHash)
	return f.insertEnvironmentDevice()
}

// returns is a fake method that reads value.
func returns[T any](value T) func() (T, error) {
	return func() (T, error) { return value, nil }
}

// done is a fake method that applies successfully.
func done() error { return nil }

// activeTurn is a fake LoadActiveTurn: the Session's active Turn, or none.
func activeTurn(turn *Turn) func() (Turn, bool, error) {
	return func() (Turn, bool, error) {
		if turn == nil {
			return Turn{}, false, nil
		}
		return *turn, true, nil
	}
}

// inputs is a fake LoadEnvironmentInput that reads states one call at a time,
// then nil.
func inputs(states ...*EnvironmentInputState) func() (*EnvironmentInputState, error) {
	return func() (*EnvironmentInputState, error) {
		var state *EnvironmentInputState
		if len(states) > 0 {
			state, states = states[0], states[1:]
		}
		return state, nil
	}
}

// collect is a fake AppendChanges that keeps the journaled changes.
func collect(changes *[]SessionChange) func([]SessionChange) error {
	return func(appended []SessionChange) error {
		*changes = append(*changes, appended...)
		return nil
	}
}

func assertCalls(t *testing.T, f *fakeTx, want ...string) {
	t.Helper()
	if strings.Join(f.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s\nwant:\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
}

var errStorage = errors.New("storage")

func TestLockedSessionPublic(t *testing.T) {
	if err := (LockedSession{}).Public(); err != nil {
		t.Fatal(err)
	}
	if err := (LockedSession{Deleted: true}).Public(); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
