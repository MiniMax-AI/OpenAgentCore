package proto

import (
	"errors"
	"regexp"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
)

// MCPHTTPServer declares public MCP tools with an explicit outbound connection origin. It is an HTTP
// server by ServerURL, or a process in the Environment when Stdio is set (origin "environment",
// ServerURL empty, no headers or bearer). Send only to a peer advertising mcp_http_tools; a transient BearerToken additionally requires
// mcp_http_bearer_auth. Required initialization requires mcp_http_required.
// Never persist or log this private request as configuration.
type MCPHTTPServer struct {
	ConnectionOrigin string    `json:"connection_origin"`
	ServerLabel      string    `json:"server_label"`
	ServerURL        string    `json:"server_url"`
	AllowedTools     *[]string `json:"allowed_tools"`
	Required         bool      `json:"required,omitempty"`
	BearerToken      *string   `json:"bearer_token,omitempty"`
	// Headers are non-secret; credentials travel only in BearerToken.
	Headers map[string]string `json:"headers,omitempty"`
	Stdio   *MCPStdio         `json:"stdio,omitempty"`
}

// MCPStdio starts an MCP server as a process of the Runtime's Environment. EnvVars
// name variables the process inherits from that Environment; values never cross the wire.
type MCPStdio struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Cwd     string   `json:"cwd"`
	EnvVars []string `json:"env_vars"`
}

var mcpEnvironmentVariable = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Valid reports a startable declaration: a nonempty command, an absolute cwd,
// variable names in EnvVars and no NUL in the command or its arguments.
func (s MCPStdio) Valid() bool {
	if strings.TrimSpace(s.Command) == "" || strings.ContainsRune(s.Command+strings.Join(s.Args, ""), 0) ||
		agentcapabilities.ValidateSourceDirectories([]string{s.Cwd}) != nil {
		return false
	}
	for _, name := range s.EnvVars {
		if !mcpEnvironmentVariable.MatchString(name) {
			return false
		}
	}
	return true
}

// ValidateConnectionOrigin rejects relocation before dispatch and adapter setup.
func (server MCPHTTPServer) ValidateConnectionOrigin(req PromptRequestPayload) error {
	switch server.ConnectionOrigin {
	case "service":
		if server.Stdio != nil {
			return errors.New("MCP stdio requires an execution environment")
		}
		if !req.DisableExecutionEnvironment || req.LocalEnvironment != nil {
			return errors.New("service-origin MCP requires a service execution host")
		}
	case "environment":
		if req.DisableExecutionEnvironment || req.LocalEnvironment == nil {
			return errors.New("environment MCP requires an execution environment")
		}
	default:
		return errors.New("MCP requires an explicit connection origin")
	}
	return nil
}
