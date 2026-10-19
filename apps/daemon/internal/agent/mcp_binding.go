package agent

import (
	"errors"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// MCPBinding is the Runtime's transient effective declaration. Adapters project
// it into their native configuration without resolving credentials or packages.
// Never serialize or log bindings: bearer values are confidential.
type MCPBinding struct {
	ServerLabel         string
	ConnectionOrigin    string
	CredentialAuthority string
	Transport           string
	ServerURL           string
	AllowedTools        *[]string
	Required            bool
	BearerToken         *string
	HTTPHeaders         map[string]string
	// Stdio retains the installed package identity for the fixed Runtime launcher.
	Stdio *proto.EnvironmentMCP
	// Process is a public stdio declaration, started natively by the Harness.
	Process *proto.MCPStdio
}

// ResolveMCPBindings combines public declarations with the frozen installation.
// Public declarations retain explicit origin and Vault authority; installed MCP
// retains Environment configuration authority. Neither may relocate implicitly.
func ResolveMCPBindings(req proto.PromptRequestPayload) ([]MCPBinding, error) {
	var bindings []MCPBinding
	if req.MCPHTTPServers != nil {
		bindings = make([]MCPBinding, 0, len(*req.MCPHTTPServers))
		for _, server := range *req.MCPHTTPServers {
			if err := server.ValidateConnectionOrigin(req); err != nil {
				return nil, err
			}
			transport := "http"
			if server.Stdio != nil {
				transport = "stdio"
			}
			item := MCPBinding{ServerLabel: server.ServerLabel, ConnectionOrigin: server.ConnectionOrigin, CredentialAuthority: "none", Transport: transport, ServerURL: server.ServerURL, Required: server.Required, BearerToken: server.BearerToken, HTTPHeaders: maps.Clone(server.Headers)}
			if server.Stdio != nil {
				process := *server.Stdio
				process.Args, process.EnvVars = slices.Clone(process.Args), slices.Clone(process.EnvVars)
				item.Process = &process
			}
			if server.AllowedTools != nil {
				names := append([]string{}, (*server.AllowedTools)...)
				item.AllowedTools = &names
			}
			if server.BearerToken != nil {
				item.CredentialAuthority = "project_vault"
			}
			bindings = append(bindings, item)
		}
	}
	if local := req.LocalEnvironment; local != nil && len(local.MCP) > 0 {
		for _, installed := range local.MCP {
			declaration := installed.Server
			item := MCPBinding{ServerLabel: declaration.Name, ConnectionOrigin: "environment", CredentialAuthority: "none", Transport: declaration.Type, ServerURL: declaration.URL, BearerToken: installed.BearerToken, HTTPHeaders: maps.Clone(declaration.HTTPHeaders)}
			if declaration.BearerTokenEnvVar != "" && installed.BearerToken == nil {
				return nil, errors.New("environment MCP credential unavailable")
			}
			if installed.BearerToken != nil || len(declaration.HTTPHeaders) > 0 || len(declaration.EnvVars) > 0 {
				item.CredentialAuthority = "environment_configuration"
			}
			if declaration.Type == "stdio" {
				value := installed
				value.Server.Args = slices.Clone(installed.Server.Args)
				value.Server.EnvVars = slices.Clone(installed.Server.EnvVars)
				item.Stdio = &value
			}
			bindings = append(bindings, item)
		}
	}
	names := map[string]bool{}
	for i := range bindings {
		item := &bindings[i]
		if item.ServerLabel == "" || strings.TrimSpace(item.ServerLabel) != item.ServerLabel || names[item.ServerLabel] {
			return nil, errors.New("ambiguous MCP identity")
		}
		names[item.ServerLabel] = true
		if item.BearerToken != nil {
			value := *item.BearerToken
			item.BearerToken = &value
		}
		if item.AllowedTools != nil {
			for _, name := range *item.AllowedTools {
				if name == "" || strings.TrimSpace(name) != name {
					return nil, errors.New("invalid MCP tool allowlist")
				}
			}
		}
		switch item.Transport {
		case "stdio":
			if (item.Stdio == nil) == (item.Process == nil) || item.BearerToken != nil || len(item.HTTPHeaders) > 0 || item.ServerURL != "" {
				return nil, errors.New("invalid MCP stdio transport")
			}
			if process := item.Process; process != nil && !process.Valid() {
				return nil, errors.New("invalid MCP stdio declaration")
			}
		case "http":
			endpoint, err := url.Parse(item.ServerURL)
			if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.User != nil || endpoint.Fragment != "" || endpoint.Opaque != "" {
				return nil, errors.New("invalid MCP endpoint")
			}
			if item.BearerToken != nil && (endpoint.Scheme != "https" || !ValidMCPHTTPBearerToken(*item.BearerToken)) {
				return nil, errors.New("invalid HTTPS MCP bearer credential")
			}
		default:
			return nil, errors.New("unsupported MCP transport")
		}
	}
	return bindings, nil
}
