package sessions

import (
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

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
