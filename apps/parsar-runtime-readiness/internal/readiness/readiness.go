// Package readiness decides whether a hosted Runtime may be reported as ready.
//
// The endpoint this package serves is the Cube template probe and the sandbox
// readiness contract (implementation specification §8.1, decision D3), and it is
// the evidence the CubeSandbox adapter turns into Info.BootstrapComplete. It
// therefore reports readiness only from the daemon's own statement that it holds
// an authenticated connection to Core, combined with the private profile the
// managed bootstrap writes. It never infers readiness from "the process started"
// or "the microVM exists".
//
// Two states are legitimately ready:
//
//   - unprovisioned: the image has no daemon profile yet, which is exactly the
//     template build. Nothing is waiting to connect, so the probe must succeed
//     for Cube to freeze the snapshot.
//   - connected: the managed bootstrap has written the private profile and the
//     daemon has authenticated to Core.
//
// Everything else is not ready, and the response body carries only a state word:
// never a credential, a device id, a path or workspace content.
package readiness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// Port is the fixed in-sandbox port of this endpoint. The Cube template
	// declares it with --expose-port and --probe, and the adapter probes it, so
	// the image, the template and the adapter must agree on the value.
	Port = 49984
	// Profile is the daemon profile a managed Runtime uses.
	Profile = "default"
	// Path is the probe path Cube requests during the template build.
	Path = "/healthz"

	// maxStateAge bounds how old a connected statement may be before it stops
	// counting as evidence. A daemon that dies without withdrawing its statement
	// leaves a file whose pid is gone, which already fails the liveness check;
	// this bound only limits pid reuse.
	maxStateAge = 24 * time.Hour

	// StateUnprovisioned is reported while the image has no daemon profile.
	StateUnprovisioned = "unprovisioned"
	// StateConnected is reported once the daemon reports an authenticated
	// connection.
	StateConnected = "connected"
	// StateWaitingForDaemon is reported while a profile exists but no
	// authenticated connection has been published.
	StateWaitingForDaemon = "waiting-for-daemon"
	// StateIncomplete is reported when the image itself is not intact.
	StateIncomplete = "incomplete"
)

// State is one decision: whether the Runtime is ready, and a state word that is
// safe to expose.
type State struct {
	Ready bool   `json:"ready"`
	State string `json:"state"`
}

type connectedState struct {
	Version   int       `json:"version"`
	Connected bool      `json:"connected"`
	DeviceID  string    `json:"device_id"`
	PID       int       `json:"pid"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Environment is the observable Runtime state. Every field has a default from the
// process environment, and tests replace them.
type Environment struct {
	// Home is PARSAR_HOME.
	Home string
	// Layout lists the paths the Runtime image guarantees. A missing one means the
	// image is not the qualified Runtime image.
	Layout []string
	// Alive reports whether a pid is running. The default uses the Unix
	// "signal 0" probe, which is what a sandbox provides.
	Alive func(pid int) bool
	// Now is the clock, for the statement age bound.
	Now func() time.Time
}

// Default reads the process environment. PARSAR_HOME is the documented Runtime
// home; the image sets it.
func Default() Environment {
	home := os.Getenv("PARSAR_HOME")
	if home == "" {
		home = "/home/runtime/.parsar"
	}
	return Environment{
		Home: home,
		Layout: []string{
			"/environment/workspace",
			"/environment/staging",
			"/environment/initialization",
			"/environment/packages",
		},
		Alive: alive,
		Now:   time.Now,
	}
}

// Evaluate reports whether this Runtime is ready. It performs no write and holds
// no state, so it is safe to poll.
func (e Environment) Evaluate() State {
	for _, path := range e.Layout {
		if _, err := os.Stat(path); err != nil {
			return State{Ready: false, State: StateIncomplete}
		}
	}
	profile := filepath.Join(e.Home, "parsar-daemon", Profile)
	if _, err := os.Stat(profile); os.IsNotExist(err) {
		// The template build has no profile directory at all: nothing has been
		// provisioned, there is no connection to wait for, and image readiness is
		// the whole contract. The manifest bootstrap creates this directory
		// before it writes the file, so its presence is the provisioned signal
		// and a lost profile can never look unprovisioned again.
		return State{Ready: true, State: StateUnprovisioned}
	} else if err != nil {
		return State{Ready: false, State: StateIncomplete}
	}
	auth := filepath.Join(profile, "auth.json")
	info, err := os.Stat(auth)
	if err != nil {
		return State{Ready: false, State: StateWaitingForDaemon}
	}
	// The managed bootstrap writes this file through envd's file API and then
	// verifies its mode with a trusted command. A wider mode means the profile is
	// not the private file the daemon contract requires, whatever else is true.
	if info.Mode().Perm() != 0o600 {
		return State{Ready: false, State: StateWaitingForDaemon}
	}
	raw, err := os.ReadFile(filepath.Join(profile, "connect.state"))
	if err != nil {
		return State{Ready: false, State: StateWaitingForDaemon}
	}
	var statement connectedState
	if json.Unmarshal(raw, &statement) != nil || statement.Version != 1 || !statement.Connected || statement.PID <= 0 {
		return State{Ready: false, State: StateWaitingForDaemon}
	}
	if e.Now().Sub(statement.UpdatedAt) > maxStateAge || strings.TrimSpace(statement.DeviceID) == "" {
		return State{Ready: false, State: StateWaitingForDaemon}
	}
	if !e.Alive(statement.PID) {
		return State{Ready: false, State: StateWaitingForDaemon}
	}
	return State{Ready: true, State: StateConnected}
}
