package agent

// WorkspaceDirectoryEntry describes an entry observed without following its final symlink.
type WorkspaceDirectoryEntry struct {
	Name      string
	Kind      string
	SizeBytes *int64
}

// WorkspaceDirectoryResult is a live, bounded observation, not a filesystem snapshot.
type WorkspaceDirectoryResult struct {
	Entries   []WorkspaceDirectoryEntry
	Truncated bool
}
