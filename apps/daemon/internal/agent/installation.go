package agent

import "context"

// Installation is an optional adapter-owned native distribution contract.
// It supplies activation paths and readiness checks, never execution or model
// configuration. Runtime owns copying, checksums, publication and installation locks.
type Installation struct {
	AgentKind   string
	Version     string
	Supported   func() bool
	Environment func(directory, node string) map[string]string
	Check       func(context.Context, string, string, []string) error
}
