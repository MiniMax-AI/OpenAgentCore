package sessions

import "context"

// Reader reads Sessions and their resources and Environment, and devices.
// Callers use it directly; no use case forwards a read.
type Reader interface {
	// AuthenticateEnvironmentExecutor checks, in one database snapshot, that
	// the credential whose SHA-256 digest is given is current for the
	// Environment and belongs to its Session's creator, and returns the
	// Session's tenant. Any other credential is ErrNotFound.
	AuthenticateEnvironmentExecutor(ctx context.Context, environment, digest string) (string, error)
	DeviceReader
	EnvironmentReader
	InputReader
	ModelExecutionReader
	SessionReader
	TurnReader
}
