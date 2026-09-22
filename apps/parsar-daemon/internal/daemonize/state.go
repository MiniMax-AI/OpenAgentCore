package daemonize

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// connectedStateVersion guards the on-disk contract read by the Runtime
// readiness endpoint. A reader that does not know a version must treat the
// Runtime as not ready rather than guess.
const connectedStateVersion = 1

// ConnectedState is the daemon's own statement that it holds an authenticated
// connection to Core. It is the single source of truth the Runtime readiness
// endpoint reads (see apps/parsar-runtime-readiness): the endpoint never derives
// readiness from "the process started" on its own.
//
// The file carries no credential material: a device id is a public identifier the
// daemon already reports in its logs, and the pid is local to the sandbox.
type ConnectedState struct {
	Version   int       `json:"version"`
	Connected bool      `json:"connected"`
	DeviceID  string    `json:"device_id"`
	PID       int       `json:"pid"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WriteConnectedState publishes the authenticated-connection state atomically, so
// a reader never observes a partial file. It is 0600 under the profile's 0700
// directory.
func WriteConnectedState(path, deviceID string) error {
	if path == "" {
		return fmt.Errorf("daemonize.WriteConnectedState: empty path")
	}
	body, err := json.Marshal(ConnectedState{
		Version: connectedStateVersion, Connected: true, DeviceID: deviceID, PID: os.Getpid(), UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("daemonize: marshal connected state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("daemonize: write temporary connected state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("daemonize: rename connected state: %w", err)
	}
	return nil
}

// RemoveConnectedState withdraws the statement. Missing files are not an error:
// a daemon that never connected has nothing to withdraw.
func RemoveConnectedState(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("daemonize: remove connected state: %w", err)
	}
	return nil
}
