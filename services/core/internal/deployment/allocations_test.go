package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

type fakeReservationTx struct {
	t                       testing.TB
	loadBoundDevice         func() (bool, error)
	insertEnvironmentDevice func(sessions.ExecutionDevice, string) error
	loadEnvironment         func() (sessions.Environment, error)
	findAllocation          func() (Allocation, bool, error)
	lockDeployment          func() (placement.Deployment, error)
	loadReserved            func() (placement.Reserved, error)
	insertAllocation        func(NewAllocation) (Allocation, error)
}

func (f *fakeReservationTx) LoadBoundDevice(context.Context) (bool, error) {
	if f.loadBoundDevice == nil {
		unexpected(f.t, "LoadBoundDevice")
	}
	return f.loadBoundDevice()
}

func (f *fakeReservationTx) InsertEnvironmentDevice(_ context.Context, device sessions.ExecutionDevice, credentialHash string) error {
	if f.insertEnvironmentDevice == nil {
		unexpected(f.t, "InsertEnvironmentDevice")
	}
	return f.insertEnvironmentDevice(device, credentialHash)
}

func (f *fakeReservationTx) LoadEnvironment(context.Context) (sessions.Environment, error) {
	if f.loadEnvironment == nil {
		unexpected(f.t, "LoadEnvironment")
	}
	return f.loadEnvironment()
}

func (f *fakeReservationTx) FindAllocation() (Allocation, bool, error) {
	if f.findAllocation == nil {
		unexpected(f.t, "FindAllocation")
	}
	return f.findAllocation()
}

func (f *fakeReservationTx) LockDeployment() (placement.Deployment, error) {
	if f.lockDeployment == nil {
		unexpected(f.t, "LockDeployment")
	}
	return f.lockDeployment()
}

func (f *fakeReservationTx) LoadReserved() (placement.Reserved, error) {
	if f.loadReserved == nil {
		unexpected(f.t, "LoadReserved")
	}
	return f.loadReserved()
}

func (f *fakeReservationTx) InsertAllocation(allocation NewAllocation) (Allocation, error) {
	if f.insertAllocation == nil {
		unexpected(f.t, "InsertAllocation")
	}
	return f.insertAllocation(allocation)
}

type fakeAllocationTx struct {
	t                 testing.TB
	loadAllocation    func() (Allocation, error)
	loadSessionDevice func() (SessionDevice, bool, error)
	settleCreation    func(Allocation) (Allocation, error)
	keep              func(Allocation) (Allocation, error)
	release           func(Allocation) (Allocation, error)
}

func (f *fakeAllocationTx) LoadAllocation() (Allocation, error) {
	if f.loadAllocation == nil {
		unexpected(f.t, "LoadAllocation")
	}
	return f.loadAllocation()
}

func (f *fakeAllocationTx) LoadSessionDevice() (SessionDevice, bool, error) {
	if f.loadSessionDevice == nil {
		unexpected(f.t, "LoadSessionDevice")
	}
	return f.loadSessionDevice()
}

func (f *fakeAllocationTx) LoadActivity(Allocation) (Activity, error) {
	unexpected(f.t, "LoadActivity")
	return Activity{}, nil
}

func (f *fakeAllocationTx) LoadRestore(Allocation) (placement.Restore, error) {
	unexpected(f.t, "LoadRestore")
	return placement.Restore{}, nil
}

func (f *fakeAllocationTx) ObserveRunning(Allocation) (Allocation, error) {
	unexpected(f.t, "ObserveRunning")
	return Allocation{}, nil
}

func (f *fakeAllocationTx) Keep(current Allocation) (Allocation, error) {
	if f.keep == nil {
		unexpected(f.t, "Keep")
	}
	return f.keep(current)
}

func (f *fakeAllocationTx) SettleCreation(current Allocation) (Allocation, error) {
	if f.settleCreation == nil {
		unexpected(f.t, "SettleCreation")
	}
	return f.settleCreation(current)
}

func (f *fakeAllocationTx) Release(current Allocation) (Allocation, error) {
	if f.release == nil {
		unexpected(f.t, "Release")
	}
	return f.release(current)
}

func (f *fakeAllocationTx) SetCompute(Allocation, ComputeChange) (Allocation, error) {
	unexpected(f.t, "SetCompute")
	return Allocation{}, nil
}

func (f *fakeAllocationTx) RecordObservation(Allocation, string) error {
	unexpected(f.t, "RecordObservation")
	return nil
}

// fakeCleanupTx is a strict allocation cleanup transaction. Its Session
// methods cover a Session without an active Turn, pending input or input
// activity change.
type fakeCleanupTx struct {
	*fakeAllocationTx
	revokeDevice         func(Allocation) error
	requestCleanup       func(Allocation) (Allocation, error)
	loadEnvironment      func() (sessions.Environment, error)
	loadEnvironmentInput func() (*sessions.EnvironmentInputState, error)
	expireEnvironment    func(string) error
	failPendingInput     func() error
	loadActiveTurn       func() (sessions.Turn, bool, error)
	cancelPendingInput   func() error
}

func (f *fakeCleanupTx) RevokeDevice(current Allocation) error {
	if f.revokeDevice == nil {
		unexpected(f.t, "RevokeDevice")
	}
	return f.revokeDevice(current)
}

func (f *fakeCleanupTx) RequestCleanup(current Allocation) (Allocation, error) {
	if f.requestCleanup == nil {
		unexpected(f.t, "RequestCleanup")
	}
	return f.requestCleanup(current)
}

func (f *fakeCleanupTx) LoadEnvironment(context.Context) (sessions.Environment, error) {
	if f.loadEnvironment == nil {
		unexpected(f.t, "LoadEnvironment")
	}
	return f.loadEnvironment()
}

func (f *fakeCleanupTx) LoadEnvironmentInput(context.Context) (*sessions.EnvironmentInputState, error) {
	if f.loadEnvironmentInput == nil {
		unexpected(f.t, "LoadEnvironmentInput")
	}
	return f.loadEnvironmentInput()
}

func (f *fakeCleanupTx) ExpireEnvironment(_ context.Context, environment string) error {
	if f.expireEnvironment == nil {
		unexpected(f.t, "ExpireEnvironment")
	}
	return f.expireEnvironment(environment)
}

func (f *fakeCleanupTx) FailPendingInput(context.Context) error {
	if f.failPendingInput == nil {
		unexpected(f.t, "FailPendingInput")
	}
	return f.failPendingInput()
}

func (f *fakeCleanupTx) LoadActiveTurn(context.Context) (sessions.Turn, bool, error) {
	if f.loadActiveTurn == nil {
		unexpected(f.t, "LoadActiveTurn")
	}
	return f.loadActiveTurn()
}

func (f *fakeCleanupTx) CancelPendingInput(context.Context) error {
	if f.cancelPendingInput == nil {
		unexpected(f.t, "CancelPendingInput")
	}
	return f.cancelPendingInput()
}

func (f *fakeCleanupTx) RecordEnvironmentFailure(context.Context, string, string, *sessions.ProvisioningFailureDetail) (time.Time, error) {
	unexpected(f.t, "RecordEnvironmentFailure")
	return time.Time{}, nil
}

func (f *fakeCleanupTx) RequestTurnCancel(context.Context, string) error {
	unexpected(f.t, "RequestTurnCancel")
	return nil
}

func (f *fakeCleanupTx) LoadTurn(context.Context, string) (sessions.Turn, error) {
	unexpected(f.t, "LoadTurn")
	return sessions.Turn{}, nil
}

func (f *fakeCleanupTx) LoadEnding(context.Context, string) (sessions.Ending, error) {
	unexpected(f.t, "LoadEnding")
	return sessions.Ending{}, nil
}

func (f *fakeCleanupTx) ApplyTurnEnd(context.Context, string, sessions.TurnEnd) error {
	unexpected(f.t, "ApplyTurnEnd")
	return nil
}

func (f *fakeCleanupTx) LoadUsage(context.Context) (json.RawMessage, error) {
	unexpected(f.t, "LoadUsage")
	return nil, nil
}

func (f *fakeCleanupTx) AppendChanges(context.Context, ...sessions.SessionChange) error {
	unexpected(f.t, "AppendChanges")
	return nil
}

// allocationOperations builds execution operations whose reservations and
// allocation changes run on the given transactions.
func allocationOperations(t *testing.T, reservation *fakeReservationTx, locked sessions.LockedSession, allocation *fakeAllocationTx) *ExecutionOperations {
	t.Helper()
	storage := &fakeExecutionStorage{t: t}
	if reservation != nil {
		storage.withReservation = func(_ context.Context, _ AllocationKey, apply func(sessions.LockedSession, ReservationTx) error) error {
			return apply(locked, reservation)
		}
	}
	if allocation != nil {
		storage.withAllocation = func(_ context.Context, _ AllocationKey, apply func(AllocationTx) error) error {
			return apply(allocation)
		}
	}
	result, err := NewExecutionOperations(newService(t, &fakeStorage{t: t}, &fakeReader{t: t}, testPublicURL), storage)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func testCredentialHash() string {
	digest := sha256.Sum256([]byte("credential"))
	return hex.EncodeToString(digest[:])
}

func hostedEnvironment(id string) func() (sessions.Environment, error) {
	return func() (sessions.Environment, error) {
		return sessions.Environment{ID: id, Status: "pending", Configuration: json.RawMessage(`{"type":"openai_hosted"}`)}, nil
	}
}

// An existing allocation replays before admission is read, so retries and
// cleanup continue while admission is closed; another installation conflicts.
func TestReserveAllocationReplaysBeforeAdmission(t *testing.T) {
	key := AllocationKey{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString()}
	installation := uuid.NewString()
	existing := Allocation{ID: uuid.NewString(), EnvironmentID: key.EnvironmentID, ProviderKey: installation}
	tx := &fakeReservationTx{t: t, loadEnvironment: hostedEnvironment(key.EnvironmentID),
		findAllocation: func() (Allocation, bool, error) { return existing, true, nil }}
	replayed, err := allocationOperations(t, tx, sessions.LockedSession{}, nil).ReserveAllocation(t.Context(), key, installation, testCredentialHash())
	if err != nil || !replayed.Replayed || replayed.ID != existing.ID {
		t.Fatal("reservation did not replay", replayed, err)
	}
	if _, err := allocationOperations(t, tx, sessions.LockedSession{}, nil).ReserveAllocation(t.Context(), key, uuid.NewString(), testCredentialHash()); !errors.Is(err, ErrAllocationConflict) {
		t.Fatal("another installation replayed the allocation", err)
	}
	deleted := &fakeReservationTx{t: t}
	if _, err := allocationOperations(t, deleted, sessions.LockedSession{Deleted: true}, nil).ReserveAllocation(t.Context(), key, installation, testCredentialHash()); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("a deleted Session reserved an allocation", err)
	}
	selfHosted := &fakeReservationTx{t: t, loadEnvironment: func() (sessions.Environment, error) {
		return sessions.Environment{ID: key.EnvironmentID, Configuration: json.RawMessage(`{"type":"self_hosted"}`)}, nil
	}}
	if _, err := allocationOperations(t, selfHosted, sessions.LockedSession{}, nil).ReserveAllocation(t.Context(), key, installation, testCredentialHash()); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("a self-hosted Environment reserved an allocation", err)
	}
}

// A fresh reservation passes admission, takes the node and generation its
// Session reserved and creates the dedicated device with the allocation.
func TestReserveAllocationAdmitsAndTakesTheReservedNode(t *testing.T) {
	key := AllocationKey{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString()}
	installation, node := uuid.NewString(), uuid.NewString()
	deployment := placement.Deployment{InstallationID: installation, Mode: "nodes", Generation: 7}
	fresh := func() *fakeReservationTx {
		return &fakeReservationTx{t: t, loadEnvironment: hostedEnvironment(key.EnvironmentID),
			findAllocation: func() (Allocation, bool, error) { return Allocation{}, false, nil },
			lockDeployment: func() (placement.Deployment, error) { return deployment, nil }}
	}
	paused := fresh()
	paused.lockDeployment = func() (placement.Deployment, error) {
		d := deployment
		d.AdmissionPaused = true
		return d, nil
	}
	if _, err := allocationOperations(t, paused, sessions.LockedSession{}, nil).ReserveAllocation(t.Context(), key, installation, testCredentialHash()); !errors.Is(err, placement.ErrAdmissionClosed) {
		t.Fatal("paused admission reserved an allocation", err)
	}
	released := fresh()
	released.loadReserved = func() (placement.Reserved, error) {
		return placement.Reserved{NodeID: node, Generation: 5, Released: true, Available: true}, nil
	}
	if _, err := allocationOperations(t, released, sessions.LockedSession{}, nil).ReserveAllocation(t.Context(), key, installation, testCredentialHash()); !errors.Is(err, placement.ErrNodeUnavailable) {
		t.Fatal("a released placement reserved an allocation", err)
	}
	var device sessions.ExecutionDevice
	var inserted NewAllocation
	tx := fresh()
	tx.loadReserved = func() (placement.Reserved, error) {
		return placement.Reserved{NodeID: node, Generation: 5, Available: true}, nil
	}
	tx.loadBoundDevice = func() (bool, error) { return false, nil }
	tx.insertEnvironmentDevice = func(d sessions.ExecutionDevice, hash string) error {
		if hash != testCredentialHash() {
			t.Fatal("device credential", hash)
		}
		device = d
		return nil
	}
	tx.insertAllocation = func(a NewAllocation) (Allocation, error) {
		inserted = a
		return Allocation{ID: a.ID, DeviceID: a.DeviceID, NodeID: a.NodeID}, nil
	}
	result, err := allocationOperations(t, tx, sessions.LockedSession{}, nil).ReserveAllocation(t.Context(), key, installation, testCredentialHash())
	if err != nil || result.Replayed || inserted.NodeID != node || inserted.Generation != 5 || inserted.ProviderKey != installation || inserted.DeviceID != device.ID || device.EnvironmentID != key.EnvironmentID {
		t.Fatal("reservation", result, inserted, device, err)
	}
}

// A live change needs the Session undeleted and bound to the allocation's
// device; settlement continues for a deleted Session. Any other owner is a
// conflict.
func TestAllocationChangesCheckTheOwner(t *testing.T) {
	owner := Allocation{ID: uuid.NewString(), DeviceID: uuid.NewString(), EnvironmentID: uuid.NewString(), TenantID: uuid.NewString(), ProviderKey: uuid.NewString()}
	stored := func(current Allocation) func() (Allocation, error) {
		return func() (Allocation, error) { return current, nil }
	}
	moved := owner
	moved.DeviceID = uuid.NewString()
	if _, err := allocationOperations(t, nil, sessions.LockedSession{}, &fakeAllocationTx{t: t, loadAllocation: stored(moved)}).SettleCreation(t.Context(), owner); !errors.Is(err, ErrAllocationConflict) {
		t.Fatal("a replaced allocation settled", err)
	}
	deleted := owner
	deleted.SessionDeleted = true
	if _, err := allocationOperations(t, nil, sessions.LockedSession{}, &fakeAllocationTx{t: t, loadAllocation: stored(deleted)}).KeepAllocation(t.Context(), owner); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("a deleted Session kept its allocation", err)
	}
	settled := &fakeAllocationTx{t: t, loadAllocation: stored(deleted), settleCreation: func(current Allocation) (Allocation, error) {
		current.CreateSettled = true
		return current, nil
	}}
	if result, err := allocationOperations(t, nil, sessions.LockedSession{}, settled).SettleCreation(t.Context(), owner); err != nil || !result.CreateSettled {
		t.Fatal("a deleted Session's creation did not settle", result, err)
	}
	unbound := &fakeAllocationTx{t: t, loadAllocation: stored(owner), loadSessionDevice: func() (SessionDevice, bool, error) {
		return SessionDevice{ID: uuid.NewString(), EnvironmentID: owner.EnvironmentID}, true, nil
	}}
	if _, err := allocationOperations(t, nil, sessions.LockedSession{}, unbound).KeepAllocation(t.Context(), owner); !errors.Is(err, ErrAllocationConflict) {
		t.Fatal("a Session bound to another device kept the allocation", err)
	}
}

// SetCompute rejects an invalid phase change before it reaches storage.
func TestSetComputeValidatesBeforeStorage(t *testing.T) {
	owner := Allocation{ID: uuid.NewString(), TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), ComputePhase: "running"}
	until := time.Now().Add(time.Hour)
	for name, call := range map[string]func(*ExecutionOperations) error{
		"skipped phase": func(o *ExecutionOperations) error {
			_, err := o.SetCompute(t.Context(), owner, "suspended", json.RawMessage(`{}`), &until, time.Second)
			return err
		},
		"state not object": func(o *ExecutionOperations) error {
			_, err := o.SetCompute(t.Context(), owner, "quiescing", json.RawMessage(`[]`), &until, time.Second)
			return err
		},
		"no retention": func(o *ExecutionOperations) error {
			_, err := o.SetCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), nil, time.Second)
			return err
		},
		"no idle timeout": func(o *ExecutionOperations) error {
			_, err := o.SetCompute(t.Context(), owner, "quiescing", json.RawMessage(`{}`), &until, 0)
			return err
		},
	} {
		if err := call(allocationOperations(t, nil, sessions.LockedSession{}, nil)); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(name, err)
		}
	}
}

// A malformed credential digest is deployment's invalid input and never
// reaches storage.
func TestReserveAllocationValidatesTheCredentialDigest(t *testing.T) {
	key := AllocationKey{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString()}
	if _, err := allocationOperations(t, nil, sessions.LockedSession{}, nil).ReserveAllocation(t.Context(), key, uuid.NewString(), "not-a-digest"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("a malformed digest reserved an allocation", err)
	}
}

// Cleanup revokes the device, then cancels a deleted Session's work or
// terminates a live Session's Environment, then requests cleanup. An absent
// creation then settles and releases the allocation, which releases its node
// placement with it.
func TestCleanupRevokesSettlesTheSessionThenReleases(t *testing.T) {
	owner := Allocation{ID: uuid.NewString(), DeviceID: uuid.NewString(), EnvironmentID: uuid.NewString(), TenantID: uuid.NewString(), ProviderKey: uuid.NewString(), State: "running"}
	for _, test := range []struct {
		name            string
		deleted, absent bool
		want            []string
	}{
		{"deleted Session with absent creation", true, true, []string{"LoadAllocation", "RevokeDevice", "LoadAllocation", "LoadActiveTurn", "CancelPendingInput", "RequestCleanup", "SettleCreation", "Release"}},
		{"live Session with expired compute", false, false, []string{"LoadAllocation", "RevokeDevice", "LoadAllocation", "LoadEnvironment", "LoadEnvironmentInput", "ExpireEnvironment", "FailPendingInput", "LoadActiveTurn", "CancelPendingInput", "LoadEnvironmentInput", "RequestCleanup"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls []string
			stored := owner
			stored.SessionDeleted, stored.Expired = test.deleted, !test.deleted
			change := func(name string, apply func(*Allocation)) func(Allocation) (Allocation, error) {
				return func(current Allocation) (Allocation, error) {
					calls = append(calls, name)
					apply(&current)
					return current, nil
				}
			}
			tx := &fakeCleanupTx{
				fakeAllocationTx: &fakeAllocationTx{t: t,
					loadAllocation: func() (Allocation, error) { calls = append(calls, "LoadAllocation"); return stored, nil },
					settleCreation: change("SettleCreation", func(a *Allocation) { a.CreateSettled = true }),
					release:        change("Release", func(a *Allocation) { a.State = "released" }),
				},
				revokeDevice:   func(Allocation) error { calls = append(calls, "RevokeDevice"); return nil },
				requestCleanup: change("RequestCleanup", func(a *Allocation) { a.State = "cleanup_pending" }),
				loadActiveTurn: func() (sessions.Turn, bool, error) {
					calls = append(calls, "LoadActiveTurn")
					return sessions.Turn{}, false, nil
				},
				cancelPendingInput: func() error { calls = append(calls, "CancelPendingInput"); return nil },
			}
			if !test.deleted {
				tx.loadEnvironment = func() (sessions.Environment, error) {
					calls = append(calls, "LoadEnvironment")
					return sessions.Environment{ID: owner.EnvironmentID, Status: "ready"}, nil
				}
				tx.loadEnvironmentInput = func() (*sessions.EnvironmentInputState, error) {
					calls = append(calls, "LoadEnvironmentInput")
					return nil, nil
				}
				tx.expireEnvironment = func(string) error { calls = append(calls, "ExpireEnvironment"); return nil }
				tx.failPendingInput = func() error { calls = append(calls, "FailPendingInput"); return nil }
			}
			storage := &fakeExecutionStorage{t: t, withAllocationCleanup: func(_ context.Context, _ AllocationKey, apply func(AllocationCleanupTx) error) error {
				return apply(tx)
			}}
			operations, err := NewExecutionOperations(newService(t, &fakeStorage{t: t}, &fakeReader{t: t}, testPublicURL), storage)
			if err != nil {
				t.Fatal(err)
			}
			cleanup, want := operations.RequestCleanup, "cleanup_pending"
			if test.absent {
				cleanup, want = operations.ReleaseAbsentCreation, "released"
			}
			result, err := cleanup(t.Context(), owner)
			if err != nil || result.State != want || !slices.Equal(calls, test.want) {
				t.Fatal("cleanup order", result.State, calls, err)
			}
		})
	}
}
