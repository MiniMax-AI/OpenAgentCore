// Package auth reads the operator device profile that oac-core-device prints
// into ~/.oac/daemon/<profile>/auth.json: the server URL, the device ID and
// its runner credential.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
)

// Profile is the on-disk representation of one device profile.
type Profile struct {
	// ServerURL is the absolute base URL the daemon dials (no
	// trailing slash). The daemon joins this with paths like
	// /agent-daemon/bootstrap.
	ServerURL string `json:"server_url"`

	// RuntimeID is the device ID. The gateway uses it verbatim as
	// device_id on WS upgrade.
	RuntimeID string `json:"runtime_id"`

	// RunnerCredential is the bearer presented on every
	// /agent-daemon/* call. Stored plaintext in a 0o600 file; the
	// server holds only the hash, so this is the only proof of
	// identity and MUST NOT be checked into VCS.
	RunnerCredential string `json:"runner_credential"`

	DeviceName string `json:"device_name,omitempty"`
}

// ErrNoProfile is returned by Load when no auth.json exists for the
// requested profile.
var ErrNoProfile = errors.New("auth: no device profile — provision one with oac-core-device")

// Load reads the profile's auth.json. Returns ErrNoProfile wrapping
// fs.ErrNotExist when the file is missing.
func Load(profile string) (Profile, error) {
	authPath, err := paths.AuthFile(profile)
	if err != nil {
		return Profile{}, err
	}
	raw, err := os.ReadFile(authPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Multi-%w so errors.Is matches both ErrNoProfile AND
			// fs.ErrNotExist.
			return Profile{}, fmt.Errorf("%w (looked at %s): %w", ErrNoProfile, authPath, err)
		}
		return Profile{}, fmt.Errorf("auth: read: %w", err)
	}
	var p Profile
	if err := json.Unmarshal(raw, &p); err != nil {
		return Profile{}, fmt.Errorf("auth: parse %s: %w", authPath, err)
	}
	return p, nil
}

// Delete removes the auth.json for a profile. Idempotent — missing
// file is not an error.
func Delete(profile string) error {
	authPath, err := paths.AuthFile(profile)
	if err != nil {
		return err
	}
	if err := os.Remove(authPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("auth: delete: %w", err)
	}
	return nil
}
