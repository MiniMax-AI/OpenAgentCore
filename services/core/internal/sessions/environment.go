package sessions

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
)

// EnvironmentReader reads Environments, their preparation and the Session
// data frozen for it.
type EnvironmentReader interface {
	// GetEnvironment reads the tenant's Environment of a Session that was not
	// publicly deleted; otherwise it is ErrNotFound.
	GetEnvironment(ctx context.Context, tenant, environment string) (Environment, error)
	// GetSessionEnvironment reads the Environment of the tenant's Session
	// that was not publicly deleted; otherwise it is ErrNotFound.
	GetSessionEnvironment(ctx context.Context, tenant, session string) (Environment, error)
	// ListEnvironmentInitializations lists, in Environment order after the
	// given Environment, a page of the live Environments whose preparation is
	// pending or running.
	ListEnvironmentInitializations(ctx context.Context, after string) ([]EnvironmentInitialization, error)
	// ReadEnvironmentSetup opens the setup frozen for the Session's
	// Environment. A missing Session is ErrNotFound and a missing credential
	// key credentialcrypto.ErrUnavailable; frozen data that does not open or
	// validate is an internal error.
	ReadEnvironmentSetup(ctx context.Context, tenant, session string) (environmentconfig.Setup, error)
	// ReadInitialEnvironmentFile opens the initial file frozen at position for
	// the Session's Environment, so an installation holds one file at a time.
	// A missing file is ErrNotFound and a missing credential key
	// credentialcrypto.ErrUnavailable; a file that does not open or match its
	// recorded size is an internal error.
	ReadInitialEnvironmentFile(ctx context.Context, tenant, session string, position int) (environmentconfig.InitialFileMetadata, []byte, error)
}

// CreatesEnvironment reports whether a Session created with the configuration
// snapshot has an Environment: a self_hosted or openai_hosted one does, and
// none or no Environment does not. Any other snapshot is ErrInvalidInput.
func CreatesEnvironment(configuration json.RawMessage) (bool, error) {
	var snapshot struct {
		Environment *struct {
			Type string `json:"type"`
		} `json:"environment"`
	}
	if err := json.Unmarshal(configuration, &snapshot); err != nil {
		return false, fmt.Errorf("%w: invalid environment configuration", ErrInvalidInput)
	}
	if snapshot.Environment == nil || snapshot.Environment.Type == "none" {
		return false, nil
	}
	switch snapshot.Environment.Type {
	case "self_hosted", "openai_hosted":
		return true, nil
	default:
		return false, fmt.Errorf("%w: unsupported environment type", ErrInvalidInput)
	}
}

// Environment retains execution ownership; its configuration is an internal snapshot, not a public response.
type Environment struct {
	Initialization string
	ID             string
	SessionID      string
	TenantID       string
	Status         string
	CreatedAt      time.Time
	Configuration  json.RawMessage
}

// EnvironmentInitialization owns preparation independently of compute ownership.
// A running record without its process-local owner is unknown, never replayable.
type EnvironmentInitialization struct {
	EnvironmentID, SessionID, TenantID, DeviceID, State, Engine string
}

// EnvironmentInputActivity is the reservation-owned override before a newer Turn exists.
type EnvironmentInputActivity struct {
	Status        string    `json:"status"`
	EnvironmentID string    `json:"environment_id,omitempty"`
	Failure       string    `json:"failure,omitempty"`
	LastActiveAt  time.Time `json:"last_active_at"`
}

const (
	EnvironmentInputPending   = "pending"
	EnvironmentInputAdmitted  = "admitted"
	EnvironmentInputExpired   = "expired"
	EnvironmentInputCancelled = "cancelled"
	EnvironmentInputFailed    = "failed"
)

// EnvironmentInputReservation is private admission state, not a public Session projection.
type EnvironmentInputReservation struct {
	ID        string
	SessionID string
	State     string
	IsInitial bool
	Inputs    []Input
	CreatedAt time.Time
	Deadline  time.Time
	SettledAt *time.Time
	Receipts  []InputReceipt
}

type EnvironmentInputWork struct{ TenantID, SessionID, ReservationID string }

// FileWriteIdentity binds a private mutation to one dedicated local Runtime. RequestSHA256 covers the canonical destination, byte count and data digest.
// The caller must qualify that binding and validate native receipts independently;
// persistence alone is neither placement authority nor permission to send bytes.
type FileWriteIdentity struct {
	ID, DeviceID, RequestSHA256 string
}

func (k FileWriteIdentity) Valid() bool {
	for _, value := range []string{k.ID, k.DeviceID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return false
		}
	}
	digest, err := hex.DecodeString(k.RequestSHA256)
	return err == nil && len(digest) == 32 && hex.EncodeToString(digest) == k.RequestSHA256
}

type EnvironmentFileWrite struct {
	Identity                 FileWriteIdentity
	EnvironmentID, SessionID string
	State                    string
	CreatedAt                time.Time
	SettledAt                *time.Time
	Replayed                 bool
}
