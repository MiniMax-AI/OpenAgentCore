package main

import (
	"context"
	"errors"
	"net/url"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
)

func (m *managedNodes) webSocketURL(publicURL string) func(context.Context, gateway.AuthenticatedRuntime) (string, error) {
	return func(ctx context.Context, auth gateway.AuthenticatedRuntime) (string, error) {
		if m == nil || (auth.RuntimeNodeID == "" && auth.RuntimeAllocationID == "") {
			return publicURL, nil
		}
		if m.runtime != nil && auth.RuntimeNodeID != "" && auth.RuntimeNodeID == m.runtime.LocalNodeID {
			return runtimeWebSocketURL(m.runtime.CoreURL)
		}
		if m.setup != nil {
			return m.setup.webSocketURL(publicURL)(ctx)
		}
		return publicURL, nil
	}
}

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
