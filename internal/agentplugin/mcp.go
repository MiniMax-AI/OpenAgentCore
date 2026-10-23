package agentplugin

import (
	"encoding/json"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"

	"golang.org/x/net/http/httpguts"
)

var environmentVariable = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func readMCP(raw json.RawMessage, files map[string][]byte) ([]proto.PluginMCPServer, error) {
	config := ".mcp.json"
	declared := len(raw) != 0
	if declared {
		if json.Unmarshal(raw, &config) != nil {
			return nil, ErrInvalid
		}
		var err error
		config, err = relativeDeclaration(config)
		if err != nil {
			return nil, err
		}
	}
	body, exists := files[config]
	if !exists && !declared {
		return nil, nil
	}
	var input struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if decodeObject(body, &input) != nil || input.Servers == nil || len(input.Servers) > 50 {
		return nil, ErrInvalid
	}
	names := make([]string, 0, len(input.Servers))
	for name := range input.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	servers := make([]proto.PluginMCPServer, 0, len(names))
	for _, name := range names {
		var server proto.PluginMCPServer
		if name == "" || len(name) > 128 || strings.ContainsAny(name, "\x00\r\n") || decodeObject(input.Servers[name], &server) != nil {
			return nil, ErrInvalid
		}
		server.Name = name
		if err := validateMCPServer(&server); err != nil {
			return nil, err
		}
		servers = append(servers, server)
	}
	return servers, nil
}

func validateMCPServer(s *proto.PluginMCPServer) error {
	if s.Type == "" && s.Command != "" {
		s.Type = "stdio"
	}
	switch s.Type {
	case "http":
		endpoint, err := url.Parse(s.URL)
		if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Fragment != "" ||
			s.Command != "" || s.Args != nil || s.EnvVars != nil || s.CWD != "" ||
			(s.BearerTokenEnvVar != "" && !environmentVariable.MatchString(s.BearerTokenEnvVar)) {
			return ErrInvalid
		}
		if !ValidHTTPHeaders(s.HTTPHeaders) {
			return ErrInvalid
		}
		for name := range s.HTTPHeaders {
			if strings.EqualFold(name, "Authorization") && s.BearerTokenEnvVar != "" {
				return ErrInvalid
			}
		}
	case "stdio":
		if strings.TrimSpace(s.Command) == "" || strings.ContainsRune(s.Command, 0) || s.URL != "" || s.BearerTokenEnvVar != "" || s.HTTPHeaders != nil {
			return ErrInvalid
		}
		for _, arg := range s.Args {
			if strings.ContainsRune(arg, 0) {
				return ErrInvalid
			}
		}
		for _, name := range s.EnvVars {
			if !environmentVariable.MatchString(name) {
				return ErrInvalid
			}
		}
		if strings.ContainsAny(s.CWD, "\\\x00\r\n") {
			return ErrInvalid
		}
		if s.CWD != "" && !path.IsAbs(s.CWD) {
			for _, part := range strings.Split(s.CWD, "/") {
				if part == ".." {
					return ErrInvalid
				}
			}
		}
	default:
		return ErrInvalid
	}
	return nil
}

// ValidHTTPHeaders reports whether every MCP header has a valid HTTP name and
// value, with names unique ignoring case.
func ValidHTTPHeaders(headers map[string]string) bool {
	seen := map[string]bool{}
	for name, value := range headers {
		lower := strings.ToLower(name)
		if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) || seen[lower] {
			return false
		}
		seen[lower] = true
	}
	return true
}
