package sessions

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// EnvironmentKey names one Environment of a tenant.
type EnvironmentKey struct{ TenantID, EnvironmentID string }

// EnvironmentConnection is the connection generation that observes an
// Environment and the last revision it committed.
type EnvironmentConnection struct {
	Generation string
	Revision   int64
}

// EnvironmentExecution is the lease-bound storage of the Environment
// execution operations.
type EnvironmentExecution interface {
	// WithInitialization runs apply in the transaction of the tenant's
	// Session, with what the Session lock shows. A malformed Session ID is a
	// missing Session.
	WithInitialization(ctx context.Context, tenant, session string, apply func(context.Context, InitializationTx, LockedSession) error) error
	// WithConnection runs apply in the transaction of the Session that owns
	// the tenant's Environment, with what the Session lock shows. An
	// Environment of a publicly deleted Session, or a missing one, is
	// ErrNotFound.
	WithConnection(ctx context.Context, tenant, environment string, apply func(context.Context, ConnectionTx, LockedSession) error) error
	// WithDeviceBinding runs apply in the transaction of the tenant's Session,
	// with what the Session lock shows.
	WithDeviceBinding(ctx context.Context, tenant, session string, apply func(context.Context, DeviceBindingTx, LockedSession) error) error
	// WithFileWriteReservation reads the tenant's Environment, then runs apply
	// in the transaction of its Session, with the Environment and what the
	// Session lock shows. An Environment of a publicly deleted Session, or a
	// missing one, is ErrNotFound.
	WithFileWriteReservation(ctx context.Context, tenant, environment string, apply func(context.Context, FileWriteReservationTx, Environment, LockedSession) error) error
	// WithFileWriteSettlement reads the file write to the tenant's
	// Environment, including one whose Session was publicly deleted, then runs
	// apply in the transaction of its Session. A malformed ID is
	// ErrInvalidInput and an unknown write ErrNotFound.
	WithFileWriteSettlement(ctx context.Context, tenant, environment, write string, apply func(context.Context, FileWriteSettlementTx) error) error
	// ListEnvironmentConnections lists, in Environment order after the given
	// Environment, or from the first one when after is empty, a page of the
	// Environments of Sessions that were not publicly deleted and that are
	// connected or have a connection generation.
	ListEnvironmentConnections(ctx context.Context, after string) ([]EnvironmentKey, error)
}

// InitializationTx is the Session transaction the Environment initialization
// operations run in.
type InitializationTx interface {
	EnvironmentFailureTx
	// LoadEnvironment reads the Session's Environment.
	LoadEnvironment(ctx context.Context) (Environment, error)
	// LoadSessionDevice reads the Runtime device bound to the Session and
	// reports whether the Session has one that still has authority.
	LoadSessionDevice(ctx context.Context) (ExecutionDevice, bool, error)
	// ClaimInitialization starts the pending preparation of the live
	// Environment and reports whether it did.
	ClaimInitialization(ctx context.Context, environment string) (bool, error)
	// CompleteInitialization completes the running preparation of the live
	// Environment and reports whether it did.
	CompleteInitialization(ctx context.Context, environment string) (bool, error)
	// FailInitialization marks the Environment's preparation failed unless it
	// completed.
	FailInitialization(ctx context.Context, environment string) error
}

// ConnectionTx is the Session transaction the Environment connection
// operations run in.
type ConnectionTx interface {
	InputActivityTx
	// LoadEnvironment reads the Session's Environment.
	LoadEnvironment(ctx context.Context) (Environment, error)
	// LoadConnection reads the Environment's connection generation and
	// reports whether it has one.
	LoadConnection(ctx context.Context, environment string) (EnvironmentConnection, bool, error)
	// ReplaceConnection starts the Environment's connection generation at
	// revision zero, replacing any earlier one.
	ReplaceConnection(ctx context.Context, environment, generation string) error
	// AdvanceConnection records the revision the current generation
	// committed.
	AdvanceConnection(ctx context.Context, environment string, revision int64) error
	// DeleteConnection discards the Environment's connection generation.
	DeleteConnection(ctx context.Context, environment string) error
	// SetConnectionStatus sets the Environment's connection status.
	SetConnectionStatus(ctx context.Context, environment, status string) error
}

// DeviceBindingTx is the Session transaction BindSessionDevice runs in.
type DeviceBindingTx interface {
	// LoadDevice reports whether device is a device of the Session's tenant
	// that was not revoked.
	LoadDevice(ctx context.Context, device string) (bool, error)
	// BindDevice binds the device to the Session. A Session bound to another
	// device, or a device dedicated to another Session's Environment, is
	// ErrDeviceBindingConflict.
	BindDevice(ctx context.Context, device string) error
}

// terminalEnvironment reports whether an Environment of status failed or
// expired, after which nothing prepares or connects it.
func terminalEnvironment(status string) bool {
	return status == "failed" || status == "expired"
}

// checkInitializationOwner confirms that the claim of a listed initialization
// still names the Session's live Environment: another Environment is
// ErrDeviceBindingConflict, and a failed or expired one ErrNotFound.
func checkInitializationOwner(owner EnvironmentInitialization, environment Environment) error {
	if environment.ID != owner.EnvironmentID {
		return ErrDeviceBindingConflict
	}
	if terminalEnvironment(environment.Status) {
		return ErrNotFound
	}
	return nil
}

// checkInitializationDevice confirms that the device the initialization was
// listed with is still the Session's device for this Environment. A Session
// without a device is ErrNotFound and another binding ErrTurnConflict.
func checkInitializationDevice(owner EnvironmentInitialization, environment Environment, bound ExecutionDevice, found bool) error {
	if !found {
		return ErrNotFound
	}
	if bound.ID != owner.DeviceID || bound.EnvironmentID != environment.ID {
		return ErrTurnConflict
	}
	return nil
}

// withInitialization runs apply on the Session's live Environment that owner
// names, in the transaction of its Session.
func (o *ExecutionOperations) withInitialization(ctx context.Context, owner EnvironmentInitialization, apply func(context.Context, InitializationTx, Environment) error) error {
	return o.storage.WithInitialization(ctx, owner.TenantID, owner.SessionID, func(ctx context.Context, tx InitializationTx, locked LockedSession) error {
		if err := locked.Public(); err != nil {
			return err
		}
		environment, err := tx.LoadEnvironment(ctx)
		if err != nil {
			return err
		}
		if err := checkInitializationOwner(owner, environment); err != nil {
			return err
		}
		return apply(ctx, tx, environment)
	})
}

// advanceInitialization checks that the listed device still owns the
// preparation, then runs one preparation transition, which reports whether it
// applied; one that did not is ErrTurnConflict.
func (o *ExecutionOperations) advanceInitialization(ctx context.Context, owner EnvironmentInitialization, transition func(context.Context, InitializationTx, Environment) (bool, error)) error {
	return o.withInitialization(ctx, owner, func(ctx context.Context, tx InitializationTx, environment Environment) error {
		bound, found, err := tx.LoadSessionDevice(ctx)
		if err != nil {
			return err
		}
		if err := checkInitializationDevice(owner, environment, bound, found); err != nil {
			return err
		}
		applied, err := transition(ctx, tx, environment)
		if err == nil && !applied {
			return ErrTurnConflict
		}
		return err
	})
}

// ClaimEnvironmentInitialization starts the listed pending preparation on its
// device.
func (o *ExecutionOperations) ClaimEnvironmentInitialization(ctx context.Context, owner EnvironmentInitialization) error {
	return o.advanceInitialization(ctx, owner, func(ctx context.Context, tx InitializationTx, environment Environment) (bool, error) {
		return tx.ClaimInitialization(ctx, environment.ID)
	})
}

// CompleteEnvironmentInitialization completes the claimed preparation.
func (o *ExecutionOperations) CompleteEnvironmentInitialization(ctx context.Context, owner EnvironmentInitialization) error {
	return o.advanceInitialization(ctx, owner, func(ctx context.Context, tx InitializationTx, environment Environment) (bool, error) {
		return tx.CompleteInitialization(ctx, environment.ID)
	})
}

// FailEnvironmentInitialization records the failure of a preparation that did
// not complete and fails the Environment, as FailEnvironment reports it. It
// records state and settles work; it never destroys compute or a workspace.
func (o *ExecutionOperations) FailEnvironmentInitialization(ctx context.Context, owner EnvironmentInitialization, failure ProvisioningFailure) error {
	return o.withInitialization(ctx, owner, func(ctx context.Context, tx InitializationTx, environment Environment) error {
		if environment.Initialization == "complete" {
			return ErrTurnConflict
		}
		if err := tx.FailInitialization(ctx, environment.ID); err != nil {
			return err
		}
		return FailEnvironment(ctx, tx, environment, failure.Reason(), failure.Detail())
	})
}

// connectionGeneration returns the canonical form of a connection generation,
// a non-nil UUID, and reports whether generation is one.
func connectionGeneration(generation string) (string, bool) {
	id, err := uuid.Parse(generation)
	if err != nil || id == uuid.Nil {
		return "", false
	}
	return id.String(), true
}

// connectionStatus is the Environment status a connection observation
// reports.
func connectionStatus(connected bool) string {
	if connected {
		return "connected"
	}
	return "disconnected"
}

// withConnection runs apply on the Environment in the transaction of its
// Session, and reports the input activity that apply changes.
func (o *ExecutionOperations) withConnection(ctx context.Context, tenant, environment string, apply func(context.Context, ConnectionTx, Environment) error) error {
	return o.storage.WithConnection(ctx, tenant, environment, func(ctx context.Context, tx ConnectionTx, locked LockedSession) error {
		if err := locked.Public(); err != nil {
			return err
		}
		current, err := tx.LoadEnvironment(ctx)
		if err != nil {
			return err
		}
		return TrackInputActivity(ctx, tx, func(ctx context.Context) error { return apply(ctx, tx, current) })
	})
}

// recordConnection sets the Environment's connection status and journals the
// matching agent.session.environment.<status> change.
func recordConnection(ctx context.Context, tx ConnectionTx, environment Environment, status string) error {
	kind, err := EnvironmentType(environment.Configuration)
	if err != nil {
		return err
	}
	if err := tx.SetConnectionStatus(ctx, environment.ID, status); err != nil {
		return err
	}
	return tx.AppendChanges(ctx, EnvironmentStateChange(environment.ID, kind, status))
}

// ReplaceEnvironmentConnection starts an ordered connection generation for
// the Environment without claiming a connection; a connected Environment
// becomes disconnected. The caller serializes replacements and never retries
// an older replacement after its successor. A failed or expired Environment is
// ErrInvalidInput.
func (o *ExecutionOperations) ReplaceEnvironmentConnection(ctx context.Context, tenant, environment, generation string) error {
	generation, ok := connectionGeneration(generation)
	if !ok {
		return ErrInvalidInput
	}
	return o.withConnection(ctx, tenant, environment, func(ctx context.Context, tx ConnectionTx, current Environment) error {
		if terminalEnvironment(current.Status) {
			return ErrInvalidInput
		}
		previous, found, err := tx.LoadConnection(ctx, current.ID)
		if err != nil {
			return err
		}
		if found && previous.Generation == generation {
			return nil
		}
		if err := tx.ReplaceConnection(ctx, current.ID, generation); err != nil {
			return err
		}
		if current.Status == "connected" {
			return recordConnection(ctx, tx, current, "disconnected")
		}
		return nil
	})
}

// ObserveEnvironmentConnection commits only a newer observation of the
// current generation, with the status change it reports. Connectivity is
// transport evidence, not native preparation readiness or process quiescence.
// An observation of a failed or expired Environment is ErrInvalidInput.
func (o *ExecutionOperations) ObserveEnvironmentConnection(ctx context.Context, tenant, environment, generation string, revision int64, connected bool) error {
	generation, ok := connectionGeneration(generation)
	if !ok || revision <= 0 {
		return ErrInvalidInput
	}
	return o.withConnection(ctx, tenant, environment, func(ctx context.Context, tx ConnectionTx, current Environment) error {
		connection, found, err := tx.LoadConnection(ctx, current.ID)
		if err != nil || !found {
			return err
		}
		if connection.Generation != generation || connection.Revision >= revision {
			return nil
		}
		if terminalEnvironment(current.Status) {
			return ErrInvalidInput
		}
		if err := tx.AdvanceConnection(ctx, current.ID, revision); err != nil {
			return err
		}
		status := connectionStatus(connected)
		if current.Status == status {
			return nil
		}
		return recordConnection(ctx, tx, current, status)
	})
}

// ReconcileEnvironmentConnections runs before a new execution owner's
// connection producers start. It discards the generations of earlier
// processes and records that their connected transport was lost. An
// Environment that is gone by the time its turn comes is skipped.
func (o *ExecutionOperations) ReconcileEnvironmentConnections(ctx context.Context) error {
	after := ""
	for {
		keys, err := o.storage.ListEnvironmentConnections(ctx, after)
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			return nil
		}
		for _, key := range keys {
			err := o.withConnection(ctx, key.TenantID, key.EnvironmentID, func(ctx context.Context, tx ConnectionTx, current Environment) error {
				if err := tx.DeleteConnection(ctx, current.ID); err != nil {
					return err
				}
				if current.Status == "connected" {
					return recordConnection(ctx, tx, current, "disconnected")
				}
				return nil
			})
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			after = key.EnvironmentID
		}
	}
}

// BindSessionDevice binds the tenant's device to the Session, including a
// publicly deleted one. A retry with the same device succeeds and another
// device is ErrDeviceBindingConflict, so a Session's filesystem never moves
// silently. A malformed device ID is ErrInvalidInput, and a revoked or unknown
// device ErrNotFound. Dispatchers read the full Session binding before
// delivery.
func (o *ExecutionOperations) BindSessionDevice(ctx context.Context, tenant, session, device string) error {
	if !validID(device) {
		return ErrInvalidInput
	}
	return o.storage.WithDeviceBinding(ctx, tenant, session, func(ctx context.Context, tx DeviceBindingTx, _ LockedSession) error {
		found, err := tx.LoadDevice(ctx, device)
		if err != nil {
			return err
		}
		if !found {
			return ErrNotFound
		}
		return tx.BindDevice(ctx, device)
	})
}
