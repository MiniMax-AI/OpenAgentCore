package sessions

import (
	"context"
	"errors"
	"testing"
	"time"
)

func (f *fakeTx) LoadFileWrite(_ context.Context, write string) (EnvironmentFileWrite, bool, error) {
	f.record("LoadFileWrite", f.loadFileWrite != nil, write)
	return f.loadFileWrite()
}

func (f *fakeTx) LoadPendingInput(context.Context) (bool, error) {
	f.record("LoadPendingInput", f.loadPendingInput != nil)
	return f.loadPendingInput()
}

func (f *fakeTx) CreateFileWrite(_ context.Context, key FileWriteIdentity) (EnvironmentFileWrite, error) {
	f.record("CreateFileWrite", f.createFileWrite != nil, key.ID, key.DeviceID)
	return f.createFileWrite()
}

func (f *fakeTx) SettleFileWrite(_ context.Context, write, state string) (EnvironmentFileWrite, error) {
	f.record("SettleFileWrite", f.settleFileWrite != nil, write, state)
	return f.settleFileWrite()
}

func (f *fakeTx) RecordFileWriteAudit(_ context.Context, write string) error {
	f.record("RecordFileWriteAudit", f.recordFileWriteAudit != nil, write)
	return f.recordFileWriteAudit()
}

func (s *fakeExecutionStorage) WithFileWriteReservation(ctx context.Context, tenant, environment string, apply func(context.Context, FileWriteReservationTx, Environment, LockedSession) error) error {
	s.environmentTx("WithFileWriteReservation").record("WithFileWriteReservation", true, tenant, environment)
	return apply(ctx, s.tx, s.environment, s.locked)
}

func (s *fakeExecutionStorage) WithFileWriteSettlement(ctx context.Context, tenant, environment, write string, apply func(context.Context, FileWriteSettlementTx) error) error {
	s.environmentTx("WithFileWriteSettlement").record("WithFileWriteSettlement", true, tenant, environment, write)
	return apply(ctx, s.tx)
}

const (
	testFileWrite = "7a1e4c2b-3d5f-4a6b-8c9d-0e1f2a3b4c5d"
	testDevice    = "5b0cf2a4-6c41-4f55-9d2f-3f6f1b1c7e10"
)

var testWriteKey = FileWriteIdentity{ID: testFileWrite, DeviceID: testDevice, RequestSHA256: credentialDigest}

func TestReserveEnvironmentFileWrite(t *testing.T) {
	reserve := "WithFileWriteReservation " + testTenant + " " + testEnvironment
	admission := []string{reserve, "LoadFileWrite " + testFileWrite, "LoadEnvironment", "LoadSessionDevice", "LoadComputeSuspension", "LoadPendingFileWrite", "LoadActiveTurn", "LoadPendingInput"}
	pending := EnvironmentFileWrite{Identity: testWriteKey, EnvironmentID: testEnvironment, SessionID: testSession, State: "pending"}
	bound := loads(ExecutionDevice{ID: testDevice, EnvironmentID: testEnvironment}, true)
	admitted := func(tx fakeTx) fakeTx {
		tx.loadFileWrite = loads(EnvironmentFileWrite{}, false)
		if tx.loadEnvironment == nil {
			tx.loadEnvironment = returns(selfHosted)
		}
		if tx.loadSessionDevice == nil {
			tx.loadSessionDevice = bound
		}
		return tx
	}
	idle := func(tx fakeTx) fakeTx {
		tx = admitted(tx)
		tx.loadComputeSuspension, tx.loadPendingFileWrite, tx.loadActiveTurn = returns(false), returns(false), activeTurn(nil)
		if tx.loadPendingInput == nil {
			tx.loadPendingInput = returns(false)
		}
		return tx
	}
	other := EnvironmentFileWrite{Identity: FileWriteIdentity{ID: testFileWrite, DeviceID: testKey, RequestSHA256: credentialDigest}, State: "pending"}
	for _, test := range []struct {
		name     string
		tx       fakeTx
		locked   LockedSession
		want     error
		replayed bool
		calls    []string
	}{
		{"reserves", idle(fakeTx{createFileWrite: returns(pending)}), LockedSession{}, nil, false, append(admission, "CreateFileWrite "+testFileWrite+" "+testDevice)},
		{"replays the same identity", fakeTx{loadFileWrite: loads(pending, true)}, LockedSession{}, nil, true, admission[:2]},
		{"another identity", fakeTx{loadFileWrite: loads(other, true)}, LockedSession{}, ErrIdempotencyConflict, false, admission[:2]},
		{"a deleted Session", fakeTx{}, LockedSession{Deleted: true}, ErrNotFound, false, admission[:1]},
		{"a replaced Environment", admitted(fakeTx{loadEnvironment: returns(Environment{ID: testKey, Status: "ready"})}), LockedSession{}, ErrInvalidInput, false, admission[:3]},
		{"a failed Environment", admitted(fakeTx{loadEnvironment: returns(Environment{ID: testEnvironment, Status: "failed"})}), LockedSession{}, ErrInvalidInput, false, admission[:3]},
		{"no device", admitted(fakeTx{loadSessionDevice: loads(ExecutionDevice{}, false)}), LockedSession{}, ErrNotFound, false, admission[:4]},
		{"another device", admitted(fakeTx{loadSessionDevice: loads(ExecutionDevice{ID: testKey, EnvironmentID: testEnvironment}, true)}), LockedSession{}, ErrDeviceBindingConflict, false, admission[:4]},
		{"suspended compute", func() fakeTx { tx := admitted(fakeTx{}); tx.loadComputeSuspension = returns(true); return tx }(), LockedSession{}, ErrTurnConflict, false, admission[:5]},
		{"pending input", idle(fakeTx{loadPendingInput: returns(true)}), LockedSession{}, ErrTurnConflict, false, admission},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := test.tx
			tx.t = t
			storage := &fakeExecutionStorage{tx: &tx, environment: selfHosted, locked: test.locked}
			write, err := executionOperations(t, storage).ReserveEnvironmentFileWrite(t.Context(), testTenant, testEnvironment, testWriteKey)
			if test.want == nil && err != nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if test.want == nil && (write.Identity != testWriteKey || write.Replayed != test.replayed) {
				t.Fatalf("write %+v", write)
			}
			assertCalls(t, &tx, test.calls...)
		})
	}
	if _, err := executionOperations(t, &fakeExecutionStorage{}).ReserveEnvironmentFileWrite(t.Context(), testTenant, testEnvironment, FileWriteIdentity{ID: testFileWrite, DeviceID: testDevice}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("no request digest: %v", err)
	}
}

func TestSettleEnvironmentFileWrite(t *testing.T) {
	settle := "WithFileWriteSettlement " + testTenant + " " + testEnvironment + " " + testFileWrite
	load := "LoadFileWrite " + testFileWrite
	settledAt := time.Unix(1, 0)
	write := func(state string) EnvironmentFileWrite {
		return EnvironmentFileWrite{Identity: testWriteKey, EnvironmentID: testEnvironment, SessionID: testSession, State: state, SettledAt: &settledAt}
	}
	for _, test := range []struct {
		name     string
		state    string
		tx       fakeTx
		want     error
		replayed bool
		calls    []string
	}{
		{"commits and audits", "committed", fakeTx{loadFileWrite: loads(write("pending"), true), settleFileWrite: returns(write("committed")), recordFileWriteAudit: done},
			nil, false, []string{settle, load, "SettleFileWrite " + testFileWrite + " committed", "RecordFileWriteAudit " + testFileWrite}},
		{"rejects without an audit", "rejected", fakeTx{loadFileWrite: loads(write("pending"), true), settleFileWrite: returns(write("rejected"))},
			nil, false, []string{settle, load, "SettleFileWrite " + testFileWrite + " rejected"}},
		{"replays the same state", "committed", fakeTx{loadFileWrite: loads(write("committed"), true)}, nil, true, []string{settle, load}},
		{"another state", "rejected", fakeTx{loadFileWrite: loads(write("committed"), true)}, ErrIdempotencyConflict, false, []string{settle, load}},
		{"another identity", "committed", fakeTx{loadFileWrite: loads(EnvironmentFileWrite{Identity: FileWriteIdentity{ID: testFileWrite, DeviceID: testKey, RequestSHA256: credentialDigest}, State: "pending"}, true)},
			ErrIdempotencyConflict, false, []string{settle, load}},
		{"an unknown write", "committed", fakeTx{loadFileWrite: loads(EnvironmentFileWrite{}, false)}, ErrNotFound, false, []string{settle, load}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := test.tx
			tx.t = t
			settled, err := executionOperations(t, &fakeExecutionStorage{tx: &tx}).SettleEnvironmentFileWrite(t.Context(), testTenant, testEnvironment, testWriteKey, test.state)
			if test.want == nil && err != nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if test.want == nil && (settled.State != test.state || settled.Replayed != test.replayed) {
				t.Fatalf("settled %+v", settled)
			}
			assertCalls(t, &tx, test.calls...)
		})
	}
	if _, err := executionOperations(t, &fakeExecutionStorage{}).SettleEnvironmentFileWrite(t.Context(), testTenant, testEnvironment, testWriteKey, "pending"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("pending state: %v", err)
	}
}
