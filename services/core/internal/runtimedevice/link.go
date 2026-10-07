package runtimedevice

import "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"

// ServeAuthority is a live Link resource and the SHA-256 hex digest of the
// credential that serves it.
type ServeAuthority struct {
	Resource       sandboxbootstrap.Resource
	CredentialHash string
}

// LinkAssignment is a Session's assignment as the Link authority reads it.
type LinkAssignment struct {
	SessionID string
	RuntimeID string
	Epoch     uint64
	Bound     bool
	// AgentHost reports that the Runtime is a live agent host, and Revision
	// is its credential revision.
	AgentHost bool
	Revision  uint64
	// Resource is the live Link resource of the Session's Environment; its
	// Kind is empty when there is none.
	Resource sandboxbootstrap.Resource
	// NetworkEnabled reports that the Environment's network access is
	// enabled.
	NetworkEnabled bool
}

// AgentHost is a live agent host's credential digest and revision.
type AgentHost struct {
	CredentialHash string
	Revision       uint64
}
