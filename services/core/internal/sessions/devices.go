package sessions

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
)

// DeviceRegistration is a Runtime device's name and the SHA-256 digest of its
// credential, as NewDeviceRegistration validates them.
type DeviceRegistration struct {
	Name           string
	CredentialHash string
}

// NewDeviceRegistration validates a device registration: a name of 1 to 256
// bytes once trimmed, and a SHA-256 credential digest in hex, which it
// normalizes to lowercase. Anything else is ErrInvalidInput.
func NewDeviceRegistration(name, credentialHash string) (DeviceRegistration, error) {
	name = strings.TrimSpace(name)
	digest, err := hex.DecodeString(credentialHash)
	if err != nil || len(digest) != 32 || name == "" || len(name) > 256 {
		return DeviceRegistration{}, fmt.Errorf("%w: device name and SHA-256 credential digest required", ErrInvalidInput)
	}
	return DeviceRegistration{Name: name, CredentialHash: hex.EncodeToString(digest)}, nil
}

// RuntimeEnrollment is the resource binding an enrolled user-managed Runtime
// receives. It never carries another secret.
type RuntimeEnrollment struct {
	DeviceID           string
	SessionID          string
	EnvironmentID      string
	WorkspaceDirectory string
}

// EnrolledRuntimeBinding identifies user-managed compute, without an
// allocation or any promise of live authorization. The Worker rechecks the
// socket's key.
type EnrolledRuntimeBinding struct {
	DeviceID, TenantID, EnvironmentID, SessionID string
}

// EnrollmentAuthority is what an executor credential authorizes when it
// enrolls a Runtime: the key and the Environment's workspace directory.
type EnrollmentAuthority struct {
	KeyID              string
	WorkspaceDirectory string
}

// DeviceReader reads Runtime devices and their Session bindings.
type DeviceReader interface {
	// GetSessionDevice reads the Runtime device bound to the Session once its
	// Environment preparation completed; before that, and without a bound
	// device, it is ErrNotFound.
	GetSessionDevice(ctx context.Context, tenant, session string) (ExecutionDevice, error)
	// GetSessionRuntimeDevice reads the Runtime device bound to the Session,
	// or ErrNotFound. It reports an authorized connection binding and does not
	// admit native execution or file access before preparation completes.
	GetSessionRuntimeDevice(ctx context.Context, tenant, session string) (ExecutionDevice, error)
	// GetDeviceCredential reads the credential the Runtime gateway
	// authenticates a device with, and reports whether the device still has
	// authority.
	GetDeviceCredential(ctx context.Context, device string) (runtimedevice.Credential, bool, error)
	// ListEnrolledRuntimeBindings lists the enrolled user-managed Runtimes of
	// live Environments.
	ListEnrolledRuntimeBindings(ctx context.Context) ([]EnrolledRuntimeBinding, error)
}

// DeviceStorage stores Runtime devices.
type DeviceStorage interface {
	// CreateDevice stores a new device of the tenant and returns it.
	CreateDevice(ctx context.Context, tenant string, registration DeviceRegistration) (ExecutionDevice, error)
	// RevokeDevice revokes the tenant's device; an unknown device is
	// ErrNotFound.
	RevokeDevice(ctx context.Context, tenant, device string) error
	// TouchDevice records that the device was seen and reports whether it
	// still has authority.
	TouchDevice(ctx context.Context, device string) (bool, error)
	// TouchAuthenticatedDevice records that the device was seen with the
	// credential and reports whether that credential still has authority.
	TouchAuthenticatedDevice(ctx context.Context, device, credentialHash string) (bool, error)
	// WithEnrollment authenticates the executor credential for the
	// Environment, then runs apply in the transaction of the Environment's
	// Session, with the Environment and what the Session lock shows. A
	// credential that does not authenticate is ErrNotFound.
	WithEnrollment(ctx context.Context, environment, credentialHash string, apply func(context.Context, EnrollmentTx, Environment, LockedSession) error) error
}

// EnrollmentTx is the Session transaction EnrollRuntime runs in.
type EnrollmentTx interface {
	// AuthorizeEnrollment rechecks, under the Session lock, that the
	// credential still authorizes enrolling a Runtime for the live self_hosted
	// Environment, and holds the key's lock until the transaction ends, so
	// revocation cannot race enrollment. Without that authority it is
	// ErrNotFound.
	AuthorizeEnrollment(ctx context.Context) (EnrollmentAuthority, error)
	// EnrollDevice creates the Environment's user-managed Runtime device for
	// the key, or returns the device the same key already enrolled. A device
	// of another key, or a revoked one, is ErrDeviceBindingConflict.
	EnrollDevice(ctx context.Context, key string) (string, error)
	// BindDevice binds the device to the Session. A Session bound to another
	// device is ErrDeviceBindingConflict.
	BindDevice(ctx context.Context, device string) error
}

// CreateDevice provisions a device for an operator. It is not a tenant-facing
// registration API.
func (s *Service) CreateDevice(ctx context.Context, tenant, name, credentialHash string) (ExecutionDevice, error) {
	registration, err := NewDeviceRegistration(name, credentialHash)
	if err != nil {
		return ExecutionDevice{}, err
	}
	return s.storage.CreateDevice(ctx, tenant, registration)
}

// RevokeDevice revokes the tenant's device.
func (s *Service) RevokeDevice(ctx context.Context, tenant, device string) error {
	return s.storage.RevokeDevice(ctx, tenant, device)
}

// TouchRuntimeHeartbeat records a Runtime connection. A device that lost its
// authority reports Deleted.
func (s *Service) TouchRuntimeHeartbeat(ctx context.Context, device string) (runtimedevice.HeartbeatStatus, error) {
	current, err := s.storage.TouchDevice(ctx, device)
	if err != nil {
		return runtimedevice.HeartbeatStatus{}, err
	}
	return heartbeatStatus(current), nil
}

// TouchAgentDaemonHeartbeat records an agent daemon heartbeat under the
// credential the gateway authenticated. A credential that lost its authority
// reports Deleted.
func (s *Service) TouchAgentDaemonHeartbeat(ctx context.Context, heartbeat runtimedevice.Heartbeat) (runtimedevice.HeartbeatStatus, error) {
	current, err := s.storage.TouchAuthenticatedDevice(ctx, heartbeat.RuntimeID, heartbeat.CredentialHash)
	if err != nil {
		return runtimedevice.HeartbeatStatus{}, err
	}
	return heartbeatStatus(current), nil
}

// heartbeatStatus is the liveness a heartbeat reports. Live connectivity
// belongs to the gateway Registry; only the last-seen time is stored.
func heartbeatStatus(current bool) runtimedevice.HeartbeatStatus {
	return runtimedevice.HeartbeatStatus{Liveness: "online", Deleted: !current}
}

// EnrollRuntime binds a user-managed Runtime to one self_hosted Environment
// with the executor credential whose digest is credentialHash. A retry keeps
// the same device and key; it cannot replace compute or adopt another native
// history.
func (s *Service) EnrollRuntime(ctx context.Context, environment, credentialHash string) (RuntimeEnrollment, error) {
	var result RuntimeEnrollment
	err := s.storage.WithEnrollment(ctx, environment, credentialHash, func(ctx context.Context, tx EnrollmentTx, current Environment, locked LockedSession) error {
		if err := locked.Public(); err != nil {
			return err
		}
		authority, err := tx.AuthorizeEnrollment(ctx)
		if err != nil {
			return err
		}
		device, err := tx.EnrollDevice(ctx, authority.KeyID)
		if err != nil {
			return err
		}
		if err := tx.BindDevice(ctx, device); err != nil {
			return err
		}
		result = RuntimeEnrollment{DeviceID: device, SessionID: current.SessionID, EnvironmentID: current.ID, WorkspaceDirectory: authority.WorkspaceDirectory}
		return nil
	})
	return result, err
}
