package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/transport"
)

// prepareConnection overlaps independent preflight work after local credentials
// and enrollment are resolved. Both workers are joined before returning, including
// on failure. Their temporary context never owns the live connection or executor.
func prepareConnection(parent context.Context, discover func(context.Context) (agentCLIDiscovery, error), bootstrap func(context.Context) (*transport.BootstrapResponse, error)) (*transport.BootstrapResponse, agentCLIDiscovery, error) {
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var agents agentCLIDiscovery
	var boot *transport.BootstrapResponse
	done := make(chan error, 2)
	go func() {
		var err error
		agents, err = discover(ctx)
		done <- err
	}()
	go func() {
		var err error
		boot, err = bootstrap(ctx)
		if err != nil {
			err = fmt.Errorf("connect: bootstrap: %w", err)
		}
		done <- err
	}()
	var result error
	for range 2 {
		if err := <-done; err != nil {
			result = errors.Join(result, err)
			cancel()
		}
	}
	if result != nil {
		return nil, nil, result
	}
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	return boot, agents, nil
}
