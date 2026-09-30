package execution

// environmentNone reports whether a Session runs through Run and input
// admission: only environment:none Sessions do. Managed and self-hosted
// Environments run through RunEnvironmentInput.
func environmentNone(snapshot Snapshot) bool {
	return snapshot.Environment != nil && snapshot.Environment.Type == "none"
}
