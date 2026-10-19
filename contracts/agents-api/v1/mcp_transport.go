package v1

// MCPTransport is the credential-free transport of a saved or effective MCP
// tool: PersistedMcpTransportResourceHttp or PersistedMcpTransportResourceStdio,
// selected by Type. A pointer keeps an empty Args or EnvVars list on the wire.
type MCPTransport struct {
	Type      string             `json:"type" binding:"required" enums:"http,stdio"`
	ServerURL string             `json:"server_url,omitempty"`
	Headers   *map[string]string `json:"headers,omitempty" extensions:"x-nullable"`
	Command   string             `json:"command,omitempty"`
	Args      *[]string          `json:"args,omitempty"`
	Cwd       string             `json:"cwd,omitempty"`
	EnvVars   *[]string          `json:"env_vars,omitempty"`
}
