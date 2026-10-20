package codex

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"
)

// errMCPBindingChanged reports that the effective native configuration moved a
// declared server's credential binding or the credential store.
var errMCPBindingChanged = errors.New("codex: effective native MCP configuration differs from the public declaration")

// verifyMCPConfig runs after the workspace trust entry is in the generated
// configuration, so config/read sees the same project layer thread/start and
// thread/resume load. It runs before any native connection that carries a Core
// credential. config/read loads the layers without MCP discovery; native
// mcpServerStatus/list instead opens eager discovery connections.
func verifyMCPConfig(ctx context.Context, rpc *JSONRPCClient, plan SessionPlan) error {
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, err := rpc.Request(check, "config/read", map[string]any{"cwd": plan.Cwd, "includeLayers": false})
	if err != nil {
		// Native configuration errors and responses can contain operator secrets.
		return errors.New("codex: cannot verify public MCP configuration")
	}
	if !matchesMCPConfig(raw, plan.mcpServers) {
		return errMCPBindingChanged
	}
	return nil
}

// matchesMCPConfig checks only what binds a Core credential to a declared
// server: where the server runs and connects, which variables carry its bearer
// and headers, and the declared tool policy. Other servers and settings follow
// native defaults.
func matchesMCPConfig(raw json.RawMessage, declared map[string]mcpServerConfig) bool {
	var response struct {
		Config struct {
			Servers         map[string]map[string]any `json:"mcp_servers"`
			CredentialStore string                    `json:"mcp_oauth_credentials_store"`
		} `json:"config"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return false
	}
	config := response.Config
	if config.CredentialStore != "file" || (len(declared) > 0 && config.Servers == nil) {
		return false
	}
	for name, expected := range declared {
		server, exists := config.Servers[name]
		if !exists || server["environment_id"] != "local" {
			return false
		}
		if expected.URL != "" {
			if server["url"] != expected.URL {
				return false
			}
		} else {
			if server["command"] != expected.Command || !matchesMCPList(server["args"], expected.Args) ||
				server["cwd"] != nilIfEmpty(expected.Cwd) || !matchesMCPList(server["env_vars"], expected.EnvVars) {
				return false
			}
		}
		if server["bearer_token_env_var"] != nilIfEmpty(expected.BearerTokenEnvVar) ||
			!matchesMCPHeaderMap(server, "http_headers", expected.HTTPHeaders) ||
			!matchesMCPHeaderMap(server, "env_http_headers", nil) {
			return false
		}
		if expected.Required && server["required"] != true {
			return false
		}
		if expected.EnabledTools != nil && !matchesEnabledTools(server["enabled_tools"], *expected.EnabledTools) {
			return false
		}
	}
	return true
}

func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// matchesEnabledTools compares sets: native enabled_tools is an allowlist, not an ordered program.
func matchesEnabledTools(actual any, expected []string) bool {
	tools, ok := actual.([]any)
	if !ok {
		return false
	}
	want := make(map[string]bool, len(expected))
	for _, tool := range expected {
		want[tool] = true
	}
	got := make(map[string]bool, len(tools))
	for _, tool := range tools {
		name, ok := tool.(string)
		if !ok {
			return false
		}
		got[name] = true
	}
	return reflect.DeepEqual(want, got)
}

func matchesMCPHeaderMap(server map[string]any, field string, expected map[string]string) bool {
	actual, present := server[field]
	if len(expected) == 0 {
		return !present || actual == nil
	}
	want := make(map[string]any, len(expected))
	for key, value := range expected {
		want[key] = value
	}
	return reflect.DeepEqual(actual, want)
}

// matchesMCPList compares an ordered string list; native omits an empty one.
func matchesMCPList(actual any, expected []string) bool {
	if len(expected) == 0 {
		return actual == nil || reflect.DeepEqual(actual, []any{})
	}
	want := make([]any, len(expected))
	for i, value := range expected {
		want[i] = value
	}
	return reflect.DeepEqual(actual, want)
}
