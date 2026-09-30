package store

// SetPublicURL records OAC_PUBLIC_URL, validated by the caller. Placement
// admits only nodes enrolled with it. Call it once, before serving requests.
func (s *Store) SetPublicURL(value string) { s.publicURL = value }
