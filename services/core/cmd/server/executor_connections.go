package main

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeenrollment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// executorConnections observes enrolled sandboxes through the Link relay.
type executorConnections struct {
	sessions sessions.Reader
	links    *relay.Relay
}

func (c executorConnections) ExecutorConnected(ctx context.Context, environment, digest string) (bool, error) {
	return runtimeenrollment.RuntimeConnected(ctx, c.sessions, c.links, environment, digest)
}
