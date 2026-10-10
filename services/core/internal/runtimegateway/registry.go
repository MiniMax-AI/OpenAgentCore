// Package gateway is the server-side hub of the agent_daemon connector.
// It owns the HTTP / WebSocket entry points the daemon dials in to,
// per-device long-lived WebSocket sessions, and a process-local
// registry of deviceID/runID → Session for routing.
//
// The package deliberately does NOT depend on the connector
// implementation — the connector imports it, not the other way around.
package runtimegateway

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
)

// ErrDeviceNotRegistered is returned by Registry lookups when a caller
// asks for a device the gateway has no live session for.
var ErrDeviceNotRegistered = errors.New("agentdaemon gateway: device not registered (offline / never connected)")

// ErrWaitForDeviceTimeout is returned by WaitForDevice when the
// deadline expires before a daemon dials in.
var ErrWaitForDeviceTimeout = errors.New("agentdaemon gateway: timed out waiting for device to register")

// Registry is the process-wide map of live daemon sessions. It is
// concurrency-safe; readers and writers live in different goroutines.
// Two O(1) indexes are maintained: byDevice (primary) and byRun (per
// Subscribe).
type Registry struct {
	mu       sync.RWMutex
	byDevice map[string]*Session
	byRun    map[string]*Session

	// waiters holds buffered(1) channels that WaitForDevice callers
	// are blocked on. Register drains the slice the moment a session
	// is inserted into byDevice. Buffered(1) so a Register that
	// happens between WaitForDevice registering and selecting on the
	// chan still wakes the waiter.
	waiters         map[string][]chan *Session
	capabilityHints chan struct{}
}

// NewRegistry returns an empty registry. The zero value would also
// work but the constructor avoids accidental nil-map panics.
func NewRegistry() *Registry {
	return &Registry{
		byDevice:        map[string]*Session{},
		byRun:           map[string]*Session{},
		waiters:         map[string][]chan *Session{},
		capabilityHints: make(chan struct{}, 1),
	}
}

// Register adds a freshly-upgraded session under its deviceID. The
// latest dial-in wins: if a session was already registered for that
// device, the old one's run index entries are evicted (the caller
// closes the displaced *Session).
func (r *Registry) Register(sess *Session) (previous *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	previous = r.byDevice[sess.DeviceID]
	if previous != nil && previous != sess {
		// Done under the registry lock so the new session doesn't
		// race against the old one's read loop on the way out.
		for runID, s := range r.byRun {
			if s == previous {
				delete(r.byRun, runID)
			}
		}
	}
	r.byDevice[sess.DeviceID] = sess

	// Signal waiters while still holding the registry lock so a
	// subsequent Deregister can't sneak in and clear byDevice before
	// the waiter resumes.
	if pending, ok := r.waiters[sess.DeviceID]; ok {
		for _, ch := range pending {
			// Non-blocking by construction — channels are buffered(1).
			select {
			case ch <- sess:
			default:
			}
		}
		delete(r.waiters, sess.DeviceID)
	}
	return previous
}

// Deregister removes a device's session if it matches the one currently
// registered. Caller passes the *Session pointer so a stale goroutine
// that wakes up after a reconnect can't evict the new session.
func (r *Registry) Deregister(sess *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.byDevice[sess.DeviceID]; ok && cur == sess {
		delete(r.byDevice, sess.DeviceID)
	}
	for runID, s := range r.byRun {
		if s == sess {
			delete(r.byRun, runID)
		}
	}
}

// LookupDevice returns the registered session for a device, or
// ErrDeviceNotRegistered when the device has never dialled in / has
// dropped.
func (r *Registry) LookupDevice(deviceID string) (*Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if sess, ok := r.byDevice[deviceID]; ok {
		return sess, nil
	}
	return nil, ErrDeviceNotRegistered
}

// LookupRun returns the session currently handling a runID, or nil
// when the run has not been subscribed yet / has completed.
func (r *Registry) LookupRun(runID string) *Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byRun[runID]
}

// AttachRun adds the runID -> session mapping. Idempotent.
func (r *Registry) AttachRun(runID string, sess *Session) {
	if runID == "" || sess == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byRun[runID] = sess
}

// DetachRun removes a runID -> session mapping.
func (r *Registry) DetachRun(runID string) {
	if runID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byRun, runID)
}

// Devices returns a snapshot of registered device ids in arbitrary order.
func (r *Registry) Devices() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.byDevice))
	for id := range r.byDevice {
		out = append(out, id)
	}
	return out
}

// PendingWaiters returns the pending WaitForDevice waiter channels
// for a given deviceID. Diagnostics only.
func (r *Registry) PendingWaiters(deviceID string) []chan *Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.waiters[deviceID]
}

// WaitForDevice blocks until a session for deviceID is registered, or
// until (timeout, ctx) bound expires. Returns the *Session on success.
//
// Fast path returns immediately if already registered. Slow path
// registers a buffered(1) channel under r.waiters[deviceID] which
// Register drains the moment a matching session arrives. timeout of
// 0 means "use the context deadline only".
func (r *Registry) WaitForDevice(ctx context.Context, deviceID string, timeout time.Duration) (*Session, error) {
	if deviceID == "" {
		return nil, ErrDeviceNotRegistered
	}

	r.mu.RLock()
	if sess, ok := r.byDevice[deviceID]; ok {
		r.mu.RUnlock()
		return sess, nil
	}
	r.mu.RUnlock()

	// Buffer = 1 so Register's non-blocking send always lands even
	// if we haven't yet entered the select below.
	waiter := make(chan *Session, 1)
	r.mu.Lock()
	// Double-check — Register could have landed between the RUnlock
	// above and the Lock here.
	if sess, ok := r.byDevice[deviceID]; ok {
		r.mu.Unlock()
		return sess, nil
	}
	r.waiters[deviceID] = append(r.waiters[deviceID], waiter)
	r.mu.Unlock()

	defer r.removeWaiter(deviceID, waiter)

	var timer *time.Timer
	var timerC <-chan time.Time
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
		timerC = timer.C
	}

	select {
	case sess := <-waiter:
		if sess == nil {
			// Register never sends nil; defensive guard in case a
			// future refactor closes the channel cleanly.
			return nil, ErrDeviceNotRegistered
		}
		return sess, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timerC:
		return nil, ErrWaitForDeviceTimeout
	}
}

// removeWaiter purges a waiter channel from the pending list. Safe to
// call after Register has already drained the slice. Idempotent.
func (r *Registry) removeWaiter(deviceID string, ch chan *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending, ok := r.waiters[deviceID]
	if !ok {
		return
	}
	filtered := pending[:0]
	for _, c := range pending {
		if c != ch {
			filtered = append(filtered, c)
		}
	}
	if len(filtered) == 0 {
		delete(r.waiters, deviceID)
	} else {
		r.waiters[deviceID] = filtered
	}
}

// CapabilityHints coalesces capability changes for the single execution Worker.
// A hint grants no execution authority; the Worker rechecks its normal gates.
func (r *Registry) CapabilityHints() <-chan struct{} { return r.capabilityHints }

func (r *Registry) observeCapabilitySnapshot(sess *Session, kinds []runtimedevice.SupportedAgentKind) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.byDevice[sess.DeviceID] != sess || sess.IsClosed() {
		return false
	}
	available := 0
	for _, kind := range kinds {
		if kind.Available {
			available++
		}
	}
	if available == 0 {
		return true
	}
	select {
	case r.capabilityHints <- struct{}{}:
	default:
	}
	return true
}
