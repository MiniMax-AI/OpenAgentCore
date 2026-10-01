package agenthost

import (
	"context"
	"errors"
	"io"
	"time"
)

// processBroker runs the commands the view's shims forward to the sandbox
// over the Process service. One broker serves a Session from its first launch
// until teardown.
type processBroker interface {
	// Start begins serving the Session.
	Start(brokerConfig) error
	// Close cancels and releases the remote operations that remain and stops
	// serving.
	Close() error
}

// brokerConfig is what a Session's broker serves.
type brokerConfig struct {
	// UID and GID are the Session's.
	UID, GID uint32
	// Names maps each shim name to the program it runs in the sandbox, found
	// on the remote PATH; Paths maps each view path the shim is bound over to
	// the same sandbox path.
	Names, Paths map[string]string
	// Pass names the Harness variables a forwarded process keeps
	// (agent.View.ForwardEnv).
	Pass []string
	// Sandbox and Tool are the Session's Environment.
	Sandbox, Tool map[string]string
	// Dial opens a Process stream on the Session's attachment.
	Dial func(context.Context) (io.ReadWriteCloser, error)
	// CancelGrace is the grace of a Cancel the broker sends on its own.
	CancelGrace time.Duration
}

// errNoBroker is unavailableBroker's Start error.
var errNoBroker = errors.New("no process broker in this build")

// unavailableBroker is the broker this build has. Its Start fails, so a
// Session admits, prepares its Executor and fails its first launch with
// ErrProcessBroker.
type unavailableBroker struct{}

func (unavailableBroker) Start(brokerConfig) error { return errNoBroker }
func (unavailableBroker) Close() error             { return nil }
