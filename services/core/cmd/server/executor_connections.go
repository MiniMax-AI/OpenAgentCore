package main

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeenrollment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// executorConnections observes enrolled executors through the Runtime gateway.
// registry is nil when this Core has no gateway; no executor is then connected.
type executorConnections struct {
	sessions sessions.Reader
	registry *runtimegateway.Registry
}

func (c executorConnections) ExecutorConnected(ctx context.Context, environment, digest string) (bool, error) {
	return runtimeenrollment.RuntimeConnected(ctx, c.sessions, c.registry, environment, digest)
}
