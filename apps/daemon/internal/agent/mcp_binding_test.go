package agent

import (
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
)

func TestMCPBindingsPreserveOriginAuthorityAndPolicy(t *testing.T) {
	token := "explicit-token"
	empty := []string{}
	for _, allow := range []*[]string{nil, &empty} {
		public := []proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "remote", ServerURL: "https://example.test/mcp", AllowedTools: allow, Required: true, BearerToken: &token}}
		got, err := ResolveMCPBindings(PrepareRequest{PromptRequestPayload: proto.PromptRequestPayload{DisableExecutionEnvironment: true, MCPHTTPServers: &public}})
		if err != nil || len(got) != 1 {
			t.Fatal("public binding unavailable", err)
		}
		b := got[0]
		if b.ConnectionOrigin != "service" || b.CredentialAuthority != "project_vault" || b.Transport != "http" || !b.Required || (b.AllowedTools == nil) != (allow == nil) || *b.BearerToken != token {
			t.Fatal("public policy or authority changed")
		}
		*b.BearerToken = "mutated"
		if token != "explicit-token" {
			t.Fatal("binding retained mutable credential pointer")
		}
	}
	installed := []EnvironmentMCP{
		{Server: agentplugin.MCPServer{Name: "stdio", Type: "stdio", Command: "node", Args: []string{"tool.js"}}, PackageRoot: "plugins/proof", InstallationRoot: "/private/installed"},
		{Server: agentplugin.MCPServer{Name: "remote", Type: "http", URL: "https://example.test/mcp", HTTPHeaders: map[string]string{"X-Selected": "literal"}}, BearerToken: &token},
		{Server: agentplugin.MCPServer{Name: "configured", Type: "stdio", Command: "node", EnvVars: []string{"TOOL_TOKEN"}}, PackageRoot: "plugins/proof", InstallationRoot: "/private/installed"},
	}
	got, err := ResolveMCPBindings(PrepareRequest{PromptRequestPayload: proto.PromptRequestPayload{LocalEnvironment: &proto.LocalEnvironment{}}, MCP: installed})
	if err != nil || len(got) != 3 {
		t.Fatal("installed bindings unavailable", err)
	}
	if got[0].ConnectionOrigin != "environment" || got[0].CredentialAuthority != "none" || got[0].Stdio.PackageRoot != "plugins/proof" || got[0].Required || got[0].AllowedTools != nil ||
		got[1].CredentialAuthority != "environment_configuration" || got[2].CredentialAuthority != "environment_configuration" {
		t.Fatal("installed identity or authority changed")
	}
	got[0].Stdio.Server.Args[0] = "mutated"
	got[1].HTTPHeaders["X-Selected"] = "mutated"
	if installed[0].Server.Args[0] != "tool.js" || installed[1].Server.HTTPHeaders["X-Selected"] != "literal" {
		t.Fatal("projection changed frozen installation")
	}
}

func TestMCPBindingsDistinguishAbsentAndEmptyProfile(t *testing.T) {
	got, err := ResolveMCPBindings(PrepareRequest{})
	if err != nil || got != nil {
		t.Fatal("absent profile changed")
	}
	empty := []proto.MCPHTTPServer{}
	got, err = ResolveMCPBindings(PrepareRequest{PromptRequestPayload: proto.PromptRequestPayload{DisableExecutionEnvironment: true, MCPHTTPServers: &empty}})
	if err != nil || got == nil || len(got) != 0 {
		t.Fatal("explicit empty profile lost ownership")
	}
}

func TestMCPBindingsRejectRelocationAndAmbiguousInstallation(t *testing.T) {
	public := []proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "remote", ServerURL: "https://example.test/mcp"}}
	local := &proto.LocalEnvironment{}
	installed := []EnvironmentMCP{{Server: agentplugin.MCPServer{Name: "remote", Type: "http", URL: "https://example.test/mcp"}}}
	for _, req := range []PrepareRequest{
		{PromptRequestPayload: proto.PromptRequestPayload{MCPHTTPServers: &public, LocalEnvironment: local}, MCP: installed},
		{PromptRequestPayload: proto.PromptRequestPayload{DisableExecutionEnvironment: true, LocalEnvironment: local}, MCP: installed},
		{PromptRequestPayload: proto.PromptRequestPayload{LocalEnvironment: local}, MCP: append(installed, installed[0])},
		{PromptRequestPayload: proto.PromptRequestPayload{LocalEnvironment: local}, MCP: []EnvironmentMCP{{Server: agentplugin.MCPServer{Name: "remote", Type: "http", URL: "https://example.test/mcp", BearerTokenEnvVar: "MISSING"}}}},
	} {
		if _, err := ResolveMCPBindings(req); err == nil {
			t.Fatal("unsupported authority accepted")
		}
	}
}

func TestPublicEnvironmentMCPRetainsVaultAuthority(t *testing.T) {
	token := "selected-vault-canary"
	names := []string{"prove"}
	public := []proto.MCPHTTPServer{{ConnectionOrigin: "environment", ServerLabel: "remote", ServerURL: "https://example.test/mcp", BearerToken: &token, AllowedTools: &names, Required: true}}
	req := PrepareRequest{PromptRequestPayload: proto.PromptRequestPayload{MCPHTTPServers: &public, LocalEnvironment: &proto.LocalEnvironment{}}}
	got, err := ResolveMCPBindings(req)
	if err != nil || len(got) != 1 {
		t.Fatal("environment binding unavailable", err)
	}
	if got[0].ConnectionOrigin != "environment" || got[0].CredentialAuthority != "project_vault" || !got[0].Required || (*got[0].AllowedTools)[0] != "prove" {
		t.Fatal("public policy changed")
	}
	*got[0].BearerToken = "mutated"
	(*got[0].AllowedTools)[0] = "mutated"
	if token != "selected-vault-canary" || names[0] != "prove" {
		t.Fatal("shared mutable authority")
	}
	for _, origin := range []string{"service", "", "unknown"} {
		public[0].ConnectionOrigin = origin
		if _, err := ResolveMCPBindings(req); err == nil {
			t.Fatal("origin silently relocated", origin)
		}
	}
	public[0].ConnectionOrigin = "environment"
	req.MCP = []EnvironmentMCP{{Server: agentplugin.MCPServer{Name: "remote", Type: "http", URL: "https://example.test/mcp"}}}
	if _, err := ResolveMCPBindings(req); err == nil {
		t.Fatal("public/Plugin identity collision accepted")
	}
	req.MCP, req.LocalEnvironment = nil, nil
	if _, err := ResolveMCPBindings(req); err == nil {
		t.Fatal("unprepared environment accepted")
	}
}

func TestPublicMCPHTTPRejectsUnsafeEndpointsAndBearers(t *testing.T) {
	resolve := func(server proto.MCPHTTPServer) error {
		servers := []proto.MCPHTTPServer{server}
		_, err := ResolveMCPBindings(PrepareRequest{PromptRequestPayload: proto.PromptRequestPayload{DisableExecutionEnvironment: true, MCPHTTPServers: &servers}})
		return err
	}
	for _, endpoint := range []string{"https://user:synthetic-secret@docs.example/mcp", "https://docs.example/mcp?token=synthetic-secret", "file:///tmp/mcp"} {
		if err := resolve(proto.MCPHTTPServer{ConnectionOrigin: "service", ServerLabel: "docs", ServerURL: endpoint}); err == nil || strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatal("unsupported endpoint was accepted or exposed", err)
		}
	}
	token := "synthetic-private-token"
	if err := resolve(proto.MCPHTTPServer{ConnectionOrigin: "service", ServerLabel: "tools", ServerURL: "http://tools.example/mcp", BearerToken: &token}); err == nil || strings.Contains(err.Error(), token) {
		t.Fatal("plaintext bearer accepted or exposed")
	}
	if err := resolve(proto.MCPHTTPServer{ConnectionOrigin: "service", ServerLabel: "tools", ServerURL: "https://tools.example/mcp", BearerToken: &token}); err != nil {
		t.Fatal("HTTPS bearer declaration rejected", err)
	}
	for _, invalid := range []string{"", "=", " has-space", "has-space ", "has space", "line\r\ninjection", "nul\x00byte", "opaque中文", "middle=padding", "punctuation:invalid"} {
		if err := resolve(proto.MCPHTTPServer{ConnectionOrigin: "service", ServerLabel: "tools", ServerURL: "https://tools.example/mcp", BearerToken: &invalid}); err == nil || err.Error() != "invalid HTTPS MCP bearer credential" {
			t.Fatal("invalid bearer value accepted or unsafe error returned")
		}
	}
}
