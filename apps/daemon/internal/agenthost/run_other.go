//go:build !linux

package agenthost

import (
	"context"
	"fmt"
)

// Open reports that the agent host needs Linux.
func Open(Config) (*Host, error) {
	return nil, fmt.Errorf("%w: open", ErrUnsupported)
}

// Run reports that the agent host needs Linux.
func (*Host) Run(context.Context, Session) error {
	return fmt.Errorf("%w: run", ErrUnsupported)
}
