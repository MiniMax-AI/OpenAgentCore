package sessions

import "time"

// EnvironmentInputActivity is the reservation-owned override before a newer Turn exists.
type EnvironmentInputActivity struct {
	Status        string    `json:"status"`
	EnvironmentID string    `json:"environment_id,omitempty"`
	Failure       string    `json:"failure,omitempty"`
	LastActiveAt  time.Time `json:"last_active_at"`
}

// EnvironmentFailure is an Environment's recorded provisioning failure. It
// makes the Session failed with this reason and last activity time.
type EnvironmentFailure struct {
	Reason   string                     `json:"reason"`
	FailedAt time.Time                  `json:"failed_at"`
	Detail   *ProvisioningFailureDetail `json:"-"`
}

// ProvisioningFailureDetail is private, fixed-category evidence from a confirmed
// initialization receipt. It never contains command text, paths or Runtime output.
type ProvisioningFailureDetail struct {
	Step     *string `json:"step"`
	Index    *int    `json:"index"`
	ExitCode *int    `json:"exit_code"`
}
