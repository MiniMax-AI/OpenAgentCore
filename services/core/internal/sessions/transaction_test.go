package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/items"
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
	loadSessionDevice        func() (ExecutionDevice, bool, error)
	claimInitialization      func() (bool, error)
	completeInitialization   func() (bool, error)
	failInitialization       func() error
	loadConnection           func() (EnvironmentConnection, bool, error)
	replaceConnection        func() error
	advanceConnection        func() error
	deleteConnection         func() error
	setConnectionStatus      func() error
	loadDevice               func() (bool, error)
	bindDevice               func() error
	authorizeEnrollment      func() (EnrollmentAuthority, error)
	enrollDevice             func() (string, error)
	loadFileWrite            func() (EnvironmentFileWrite, bool, error)
	loadPendingInput         func() (bool, error)
	createFileWrite          func() (EnvironmentFileWrite, error)
	settleFileWrite          func() (EnvironmentFileWrite, error)
	recordFileWriteAudit     func() error

	lockProject                   func() (bool, error)
	listExecutorCredentials       func() ([]ExecutorCredential, error)
	authenticateExecutor          func() (bool, error)
	issueExecutorCredential       func(ExecutorCredentialGrant) (IssuedExecutorCredential, error)
	rotateExecutorCredential      func(digest string) (IssuedExecutorCredential, error)
	revokeExecutorCredential      func() error
	recordExecutorCredentialAudit func() error
	loadSessionCreator            func() (identity.Subject, bool, error)

	loadJournalTurn        func() (JournalTurn, bool, error)
	matchEvents            func() (bool, error)
	insertEvents           func() error
	insertEvent            func() error
	countEvents            func() error
	loadEventSources       func() ([]Source, error)
	loadInputSource        func() (Source, error)
	putTurnUsage           func(v1.TokenUsage) error
	loadItem               func() (items.Stored, error)
	putItem                func(items.Change) (*int32, error)
	loadRootAgent          func() (string, error)
	loadNativeSubagent     func(native string) (NativeSubagent, bool, error)
	putSubagentIdentity    func(SubagentIdentity) (string, error)
	publishSubagent        func() error
	loadPublicSubagent     func() (v1.Subagent, error)
	putSubagentEffect      func() (bool, error)
	applySubagentLifecycle func(SubagentLifecycle) error
	loadChildTurn          func() (ChildTurn, bool, error)
	putChildTurn           func(ChildTurn) error
	recordTerminalActivity func() error
	loadChildItem          func() (StoredChildItem, bool, error)
	putChildItem           func(ChildItem) error
}

var (
	_ EnvironmentTerminationTx = (*fakeTx)(nil)
	_ InputStartTx             = (*fakeTx)(nil)
	_ ComputeAdmissionTx       = (*fakeTx)(nil)
	_ EnvironmentDeviceTx      = (*fakeTx)(nil)
	_ TurnJournalTx            = (*fakeTx)(nil)
	_ TurnEventTx              = (*fakeTx)(nil)
	_ InputProjectionTx        = (*fakeTx)(nil)
	_ InitializationTx         = (*fakeTx)(nil)
	_ ConnectionTx             = (*fakeTx)(nil)
	_ DeviceBindingTx          = (*fakeTx)(nil)
	_ EnrollmentTx             = (*fakeTx)(nil)
	_ FileWriteReservationTx   = (*fakeTx)(nil)
	_ FileWriteSettlementTx    = (*fakeTx)(nil)

	_ EnvironmentExecutorCredentialTx = (*fakeTx)(nil)
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

func (f *fakeTx) LoadJournalTurn(_ context.Context, turn string) (JournalTurn, bool, error) {
	f.record("LoadJournalTurn", f.loadJournalTurn != nil, turn)
	return f.loadJournalTurn()
}

func (f *fakeTx) MatchEvents(_ context.Context, turn string, first int32, events []ExecutionEvent) (bool, error) {
	f.record("MatchEvents", f.matchEvents != nil, turn, fmt.Sprint(first), fmt.Sprint(len(events)))
	return f.matchEvents()
}

func (f *fakeTx) InsertEvents(_ context.Context, turn string, first int32, events []ExecutionEvent) error {
	f.record("InsertEvents", f.insertEvents != nil, turn, fmt.Sprint(first), fmt.Sprint(len(events)))
	return f.insertEvents()
}

func (f *fakeTx) InsertEvent(_ context.Context, turn string, ordinal int32, event ExecutionEvent) error {
	f.record("InsertEvent", f.insertEvent != nil, turn, fmt.Sprint(ordinal), event.Kind)
	return f.insertEvent()
}

func (f *fakeTx) CountEvents(_ context.Context, turn string, count int32, size int64) error {
	f.record("CountEvents", f.countEvents != nil, turn, fmt.Sprint(count), fmt.Sprint(size))
	return f.countEvents()
}

func (f *fakeTx) LoadEventSources(_ context.Context, turn string, first int32) ([]Source, error) {
	f.record("LoadEventSources", f.loadEventSources != nil, turn, fmt.Sprint(first))
	return f.loadEventSources()
}

func (f *fakeTx) LoadInputSource(_ context.Context, sequence int64) (Source, error) {
	f.record("LoadInputSource", f.loadInputSource != nil, fmt.Sprint(sequence))
	return f.loadInputSource()
}

func (f *fakeTx) PutTurnUsage(_ context.Context, turn string, usage v1.TokenUsage) error {
	f.record("PutTurnUsage", f.putTurnUsage != nil, turn)
	return f.putTurnUsage(usage)
}

func (f *fakeTx) LoadItem(_ context.Context, turn string, update items.Update) (items.Stored, error) {
	f.record("LoadItem", f.loadItem != nil, turn, update.Item.ID)
	return f.loadItem()
}

func (f *fakeTx) PutItem(_ context.Context, turn string, _ time.Time, change items.Change) (*int32, error) {
	f.record("PutItem", f.putItem != nil, turn, change.Item.ID)
	return f.putItem(change)
}

func (f *fakeTx) LoadRootAgent(context.Context) (string, error) {
	f.record("LoadRootAgent", f.loadRootAgent != nil)
	return f.loadRootAgent()
}

func (f *fakeTx) LoadNativeSubagent(_ context.Context, native string) (NativeSubagent, bool, error) {
	f.record("LoadNativeSubagent", f.loadNativeSubagent != nil, native)
	return f.loadNativeSubagent(native)
}

func (f *fakeTx) PutSubagentIdentity(_ context.Context, identity SubagentIdentity) (string, error) {
	f.record("PutSubagentIdentity", f.putSubagentIdentity != nil, identity.NativeID)
	return f.putSubagentIdentity(identity)
}

func (f *fakeTx) PublishSubagent(_ context.Context, id string, _, _ *string) error {
	f.record("PublishSubagent", f.publishSubagent != nil, id)
	return f.publishSubagent()
}

func (f *fakeTx) LoadPublicSubagent(_ context.Context, id string) (v1.Subagent, error) {
	f.record("LoadPublicSubagent", f.loadPublicSubagent != nil, id)
	return f.loadPublicSubagent()
}

func (f *fakeTx) PutSubagentEffect(_ context.Context, effect string, _ json.RawMessage) (bool, error) {
	f.record("PutSubagentEffect", f.putSubagentEffect != nil, effect)
	return f.putSubagentEffect()
}

func (f *fakeTx) ApplySubagentLifecycle(_ context.Context, id string, lifecycle SubagentLifecycle) error {
	f.record("ApplySubagentLifecycle", f.applySubagentLifecycle != nil, id, lifecycle.Status)
	return f.applySubagentLifecycle(lifecycle)
}

func (f *fakeTx) LoadChildTurn(_ context.Context, subagent, id string) (ChildTurn, bool, error) {
	f.record("LoadChildTurn", f.loadChildTurn != nil, subagent, id)
	return f.loadChildTurn()
}

func (f *fakeTx) PutChildTurn(_ context.Context, turn ChildTurn) error {
	f.record("PutChildTurn", f.putChildTurn != nil, turn.ID, turn.Status)
	return f.putChildTurn(turn)
}

func (f *fakeTx) RecordTerminalActivity(context.Context) error {
	f.record("RecordTerminalActivity", f.recordTerminalActivity != nil)
	return f.recordTerminalActivity()
}

func (f *fakeTx) LoadChildItem(_ context.Context, subagent, id string, _ json.RawMessage) (StoredChildItem, bool, error) {
	f.record("LoadChildItem", f.loadChildItem != nil, subagent, id)
	return f.loadChildItem()
}

func (f *fakeTx) PutChildItem(_ context.Context, item ChildItem) error {
	f.record("PutChildItem", f.putChildItem != nil, item.ID, fmt.Sprint(item.Position))
	return f.putChildItem(item)
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
