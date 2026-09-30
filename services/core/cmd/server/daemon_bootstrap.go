package main

import (
	"errors"
	"net/url"
)

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
