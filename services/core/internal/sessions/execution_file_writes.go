package sessions

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/writeaudit"
)

// FileWriteTx reads the file writes to one Environment inside its Session's
// transaction.
type FileWriteTx interface {
	// LoadFileWrite reads the write with the ID to the Environment and reports
	// whether it exists.
	LoadFileWrite(ctx context.Context, write string) (EnvironmentFileWrite, bool, error)
}

// FileWriteReservationTx is the Session transaction a file write is reserved
// in.
type FileWriteReservationTx interface {
	FileWriteTx
	ComputeAdmissionTx
	InputStartTx
	// LoadEnvironment reads the Session's Environment.
	LoadEnvironment(ctx context.Context) (Environment, error)
	// LoadSessionDevice reads the Runtime device bound to the Session and
	// reports whether the Session has one that still has authority.
	LoadSessionDevice(ctx context.Context) (ExecutionDevice, bool, error)
	// LoadPendingInput reports whether Environment input reserved for the
	// Session is pending.
	LoadPendingInput(ctx context.Context) (bool, error)
	// CreateFileWrite stores the pending write to the Environment with the
	// write-audit source in ctx, if any, and returns it.
	CreateFileWrite(ctx context.Context, key FileWriteIdentity) (EnvironmentFileWrite, error)
}

// FileWriteSettlementTx is the Session transaction a file write is settled in.
type FileWriteSettlementTx interface {
	FileWriteTx
	// SettleFileWrite settles the pending write in the state and returns it.
	SettleFileWrite(ctx context.Context, write, state string) (EnvironmentFileWrite, error)
	// RecordFileWriteAudit records the upload_file write audit of the write
	// under the write-audit source stored when it was reserved. A write
	// reserved without one records nothing.
	RecordFileWriteAudit(ctx context.Context, write string) error
}

// ReserveEnvironmentFileWrite persists a file write's intent before it is
// dispatched to the Environment's Runtime device. A retry with the same
// identity replays the earlier write, Replayed, and never authorizes sending
// an unknown write again; another identity for the ID is
// ErrIdempotencyConflict. The write-audit source in ctx, when present, must be
// valid for the tenant; it is stored with the intent and audited when the
// write commits.
func (o *ExecutionOperations) ReserveEnvironmentFileWrite(ctx context.Context, tenant, environment string, key FileWriteIdentity) (EnvironmentFileWrite, error) {
	if !key.Valid() {
		return EnvironmentFileWrite{}, ErrInvalidInput
	}
	var result EnvironmentFileWrite
	err := o.storage.WithFileWriteReservation(ctx, tenant, environment, func(ctx context.Context, tx FileWriteReservationTx, owner Environment, locked LockedSession) error {
		if source, ok := writeaudit.FromContext(ctx); ok {
			if err := source.Validate(tenant); err != nil {
				return err
			}
		}
		if err := locked.Public(); err != nil {
			return err
		}
		previous, found, err := tx.LoadFileWrite(ctx, key.ID)
		if err != nil {
			return err
		}
		if found {
			if previous.Identity != key {
				return ErrIdempotencyConflict
			}
			previous.Replayed = true
			result = previous
			return nil
		}
		if err := admitFileWrite(ctx, tx, owner, key); err != nil {
			return err
		}
		result, err = tx.CreateFileWrite(ctx, key)
		return err
	})
	if err != nil {
		return EnvironmentFileWrite{}, err
	}
	return result, nil
}

// admitFileWrite admits a new write to the Session's live Environment through
// the device the key names, dedicated to that Environment, while the Session
// is idle with no pending input. A failed or expired Environment is
// ErrInvalidInput, a Session without a device ErrNotFound, another device
// ErrDeviceBindingConflict, and work in progress ErrTurnConflict.
func admitFileWrite(ctx context.Context, tx FileWriteReservationTx, owner Environment, key FileWriteIdentity) error {
	current, err := tx.LoadEnvironment(ctx)
	if err != nil {
		return err
	}
	if current.ID != owner.ID || terminalEnvironment(current.Status) {
		return ErrInvalidInput
	}
	bound, found, err := tx.LoadSessionDevice(ctx)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	if bound.ID != key.DeviceID || bound.EnvironmentID != owner.ID {
		return ErrDeviceBindingConflict
	}
	if err := CheckComputeAdmission(ctx, tx); err != nil {
		return err
	}
	if err := CheckInputStart(ctx, tx); err != nil {
		return err
	}
	pending, err := tx.LoadPendingInput(ctx)
	if err != nil {
		return err
	}
	if pending {
		return ErrTurnConflict
	}
	return nil
}

// SettleEnvironmentFileWrite settles a pending write as committed or rejected
// from an independently validated exact receipt. A missing receipt,
// cancellation or owner retirement is not a rejected upload, so the caller
// leaves the write pending. Settling it again in the same state replays it,
// Replayed; another identity or state is ErrIdempotencyConflict. It also
// settles a write whose Session was publicly deleted. A committed write
// records its upload_file write audit.
func (o *ExecutionOperations) SettleEnvironmentFileWrite(ctx context.Context, tenant, environment string, key FileWriteIdentity, state string) (EnvironmentFileWrite, error) {
	if !key.Valid() || (state != "committed" && state != "rejected") {
		return EnvironmentFileWrite{}, ErrInvalidInput
	}
	var result EnvironmentFileWrite
	err := o.storage.WithFileWriteSettlement(ctx, tenant, environment, key.ID, func(ctx context.Context, tx FileWriteSettlementTx) error {
		current, found, err := tx.LoadFileWrite(ctx, key.ID)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		if current.Identity != key || (current.State != "pending" && current.State != state) {
			return ErrIdempotencyConflict
		}
		if current.State == state {
			current.Replayed = true
			result = current
			return nil
		}
		if result, err = tx.SettleFileWrite(ctx, key.ID, state); err != nil {
			return err
		}
		if state == "committed" {
			return tx.RecordFileWriteAudit(ctx, key.ID)
		}
		return nil
	})
	if err != nil {
		return EnvironmentFileWrite{}, err
	}
	return result, nil
}
