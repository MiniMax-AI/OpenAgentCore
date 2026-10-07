package relay

import (
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// The per-link and per-resource bounds the tests fill.
const (
	MaxStreams     = maxStreams
	MaxServeHellos = maxServeHellos
)

// SetLimits lowers rl's capacity for resources, attachments and attach links.
func SetLimits(rl *Relay, resources, attachments, attachLinks int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.maxResources, rl.maxAttachments, rl.maxAttachLinks = resources, attachments, attachLinks
}

// Held reports the resources, attachment slots and attach links rl holds.
func Held(rl *Relay) (resources, attachments, attachLinks int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.resources), rl.held, rl.attachLinks
}

// Lease reports the lease of the attachment rl holds under id, or the zero
// time when it holds none.
func Lease(rl *Relay, id sandboxwire.ID) time.Time {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if a := rl.attachments[id]; a != nil {
		return a.lease
	}
	return time.Time{}
}
