//go:build !linux

package agenthost

import "context"

// Run reports that the agent host needs Linux.
func Run(context.Context, Config, Session) error {
	return &Error{Kind: ErrUnsupported, Op: "run"}
}

// Sweep reports that the agent host needs Linux.
func Sweep(Config) error {
	return &Error{Kind: ErrUnsupported, Op: "sweep"}
}
