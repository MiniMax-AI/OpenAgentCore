//go:build !linux

package agenthost

import "context"

// Open reports that the agent host needs Linux.
func Open(Config) (*Host, error) {
	return nil, &Error{Kind: ErrUnsupported, Op: "open"}
}

// Run reports that the agent host needs Linux.
func (*Host) Run(context.Context, Session) error {
	return &Error{Kind: ErrUnsupported, Op: "run"}
}
