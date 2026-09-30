package sessions

import "context"

// EnvironmentDeviceTx is the Session transaction CreateEnvironmentDevice runs
// in.
type EnvironmentDeviceTx interface {
	// LoadBoundDevice reports whether the Session is bound to a Runtime device
	// that still has authority.
	LoadBoundDevice(ctx context.Context) (bool, error)
	// InsertEnvironmentDevice inserts device, with the credential hash, as the
	// dedicated Runtime device of the Session's hosted Environment and binds
	// it to the Session. An Environment that already has a device or cannot
	// take one is ErrDeviceBindingConflict.
	InsertEnvironmentDevice(ctx context.Context, device ExecutionDevice, credentialHash string) error
}

// CreateEnvironmentDevice creates the dedicated Runtime device of the
// Session's hosted Environment and binds it to the Session. A Session that is
// already bound to a device is ErrDeviceBindingConflict, so an existing
// credential is never widened.
func CreateEnvironmentDevice(ctx context.Context, tx EnvironmentDeviceTx, device ExecutionDevice, credentialHash string) error {
	bound, err := tx.LoadBoundDevice(ctx)
	if err != nil {
		return err
	}
	if bound {
		return ErrDeviceBindingConflict
	}
	return tx.InsertEnvironmentDevice(ctx, device, credentialHash)
}
