package main

import (
	"context"
	"errors"
	"net/url"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
)

// webSocketURL answers daemon bootstrap. Every daemon uses the one URL derived
// from AGENTS_API_PUBLIC_URL, except a legacy embedded node's own Runtime.
func (m *managedNodes) webSocketURL(publicURL string) func(context.Context, gateway.AuthenticatedRuntime) (string, error) {
	return func(ctx context.Context, auth gateway.AuthenticatedRuntime) (string, error) {
		if m != nil && m.runtime != nil && auth.RuntimeNodeID != "" && auth.RuntimeNodeID == m.runtime.LocalNodeID {
			return runtimeWebSocketURL(m.runtime.CoreURL)
		}
		return publicURL, nil
	}
}

// runtimeWebSocketURL derives the daemon WebSocket URL from a Core origin or
// its /api/v1 base.
func runtimeWebSocketURL(coreURL string) (string, error) {
	u, err := url.Parse(coreURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("managed Runtime Core address is unavailable")
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", errors.New("managed Runtime Core address is unavailable")
	}
	u.Path = "/api/v1/agent-daemon/ws"
	return u.String(), nil
}
