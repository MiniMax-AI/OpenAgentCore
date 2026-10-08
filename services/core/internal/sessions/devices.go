package sessions

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
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

// SandboxResource is a live Link resource. Quiesced compute is between a
// quiesce and the wake that resumes it.
type SandboxResource struct {
	Resource sandboxbootstrap.Resource
	Quiesced bool
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
	// ArchivedCancellationReceipt reads the receipt window that archiving a
	// Session leaves the device's exact authenticated delivery of one of
	// runIDs; without one it is the zero receipt.
	ArchivedCancellationReceipt(ctx context.Context, device, credentialHash string, runIDs []string) (runtimedevice.ArchivedCancellationReceipt, error)
	// ListLiveSandboxResources lists the live Link resources.
	ListLiveSandboxResources(ctx context.Context) ([]SandboxResource, error)
	// GetEnvironmentResource reads the live Link resource of the tenant's
	// Environment; without one it is ErrNotFound.
	GetEnvironmentResource(ctx context.Context, tenant, environment string) (runtimedevice.ServeAuthority, error)
	// GetSessionExecutionBinding reads the Runtime device that executes the
	// Session's Turns, with the native session that continues its history,
	// once its Environment preparation completed; before that, and without an
	// authorized bound device, it is ErrNotFound.
	GetSessionExecutionBinding(ctx context.Context, tenant, session string) (ExecutionBinding, error)
	// ListAgentHosts lists the unrevoked agent hosts that may run the
	// tenant's Sessions, in ID order.
	ListAgentHosts(ctx context.Context, tenant string) ([]ExecutionDevice, error)
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
	// credential still authorizes enrolling a sandbox for the live
	// self_hosted Environment, returns its key and holds the key's lock until
	// the transaction ends, so revocation cannot race enrollment. Without
	// that authority it is ErrNotFound.
	AuthorizeEnrollment(ctx context.Context) (string, error)
	// EnrollSandbox records the Environment's enrollment by the key and
	// returns its Link resource, or the resource the same key already
	// enrolled. An enrollment by another key is ErrDeviceBindingConflict.
	EnrollSandbox(ctx context.Context, key string) (sandboxbootstrap.Resource, error)
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

// EnrollRuntime enrolls a user-managed sandbox for one self_hosted
// Environment with the executor credential whose digest is credentialHash,
// and returns the Link resource the sandbox serves with that credential. The
// first key to enroll keeps the Environment: a retry with it returns the
// same resource, and another key is ErrDeviceBindingConflict. Placement binds
// the Session to an agent host once the sandbox serves.
func (s *Service) EnrollRuntime(ctx context.Context, environment, credentialHash string) (sandboxbootstrap.Resource, error) {
	var result sandboxbootstrap.Resource
	err := s.storage.WithEnrollment(ctx, environment, credentialHash, func(ctx context.Context, tx EnrollmentTx, _ Environment, locked LockedSession) error {
		if err := locked.Public(); err != nil {
			return err
		}
		key, err := tx.AuthorizeEnrollment(ctx)
		if err != nil {
			return err
		}
		result, err = tx.EnrollSandbox(ctx, key)
		return err
	})
	return result, err
}
