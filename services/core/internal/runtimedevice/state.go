// Package device defines persistence data shared by daemon gateways and their stores.
package runtimedevice

import "github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"

// HeartbeatStatus is the post-heartbeat liveness the runner uses to
// detect state changes.
type HeartbeatStatus struct {
	// Deleted is true when the heartbeat UPDATE matched zero rows,
	// meaning the runtime was soft-deleted (or never existed). The
	// gateway uses this to send a permanent WS close frame so the
	// daemon stops reconnecting.
	Deleted bool
}

// Heartbeat is the WebSocket daemon heartbeat
// payload after gateway normalization.
type Heartbeat struct {
	RuntimeID string
	// CredentialHash comes from gateway authentication, never a daemon frame.
	CredentialHash      string
	SupportedAgentKinds []proto.SupportedAgentKind
}
