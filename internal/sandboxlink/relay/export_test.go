package relay

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
