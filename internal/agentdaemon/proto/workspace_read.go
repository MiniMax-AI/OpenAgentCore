package proto

const (
	TypeWorkspaceRead            = "workspace_read"
	TypeWorkspaceReadResult      = "workspace_read_result"
	WorkspaceReadMaxRequestBytes = 8 << 10
	WorkspaceReadMaxIDBytes      = 128
	// WorkspaceReadNotDirectory rejects a directory read whose own path is
	// missing, a regular file or a symbolic link. The link is never followed.
	WorkspaceReadNotDirectory = "not_directory"
)

// WorkspaceReadPayload lists one directory of an existing resource on the current daemon connection.
type WorkspaceReadPayload struct {
	Handle        string `json:"handle,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	EnvironmentID string `json:"environment_id"`
	Path          string `json:"path"`
	MaxEntries    int    `json:"max_entries,omitempty"`
}

// WorkspaceReadResultPayload never infers settlement from local process exit.
type WorkspaceReadResultPayload struct {
	Outcome           string                    `json:"outcome"`
	CloseAcknowledged bool                      `json:"close_acknowledged,omitempty"`
	ErrorCode         string                    `json:"error_code,omitempty"`
	Directory         *WorkspaceDirectoryResult `json:"directory,omitempty"`
}
