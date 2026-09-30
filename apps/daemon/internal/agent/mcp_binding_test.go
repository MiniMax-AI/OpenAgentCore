package agent

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
)

func TestMCPBindingsPreserveOriginAuthorityAndPolicy(t *testing.T) {
	token := "explicit-token"
	empty := []string{}
	for _, allow := range []*[]string{nil, &empty} {
		public := []proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "remote", ServerURL: "https://example.test/mcp", AllowedTools: allow, Required: true, BearerToken: &token}}
		got, err := ResolveMCPBindings(proto.PromptRequestPayload{DisableExecutionEnvironment: true, MCPHTTPServers: &public})
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
	local := &proto.LocalEnvironment{NetworkAccess: "enabled", MCP: []proto.EnvironmentMCP{
		{Server: agentplugin.MCPServer{Name: "stdio", Type: "stdio", Command: "node", Args: []string{"tool.js"}}, PackageRoot: "plugins/proof", InstallationRoot: "/private/installed"},
		{Server: agentplugin.MCPServer{Name: "remote", Type: "http", URL: "https://example.test/mcp", HTTPHeaders: map[string]string{"X-Selected": "literal"}}, BearerToken: &token},
	}}
	got, err := ResolveMCPBindings(proto.PromptRequestPayload{LocalEnvironment: local})
	if err != nil || len(got) != 2 {
		t.Fatal("installed bindings unavailable", err)
	}
	if got[0].ConnectionOrigin != "environment" || got[0].CredentialAuthority != "none" || got[0].Stdio.PackageRoot != "plugins/proof" || got[0].Required || got[0].AllowedTools != nil || got[1].CredentialAuthority != "environment_configuration" {
		t.Fatal("installed identity or authority changed")
	}
	got[0].Stdio.Server.Args[0] = "mutated"
	got[1].HTTPHeaders["X-Selected"] = "mutated"
	if local.MCP[0].Server.Args[0] != "tool.js" || local.MCP[1].Server.HTTPHeaders["X-Selected"] != "literal" {
		t.Fatal("projection changed frozen installation")
	}
}

func TestMCPBindingsDistinguishAbsentAndEmptyProfile(t *testing.T) {
	got, err := ResolveMCPBindings(proto.PromptRequestPayload{})
	if err != nil || got != nil {
		t.Fatal("absent profile changed")
	}
	empty := []proto.MCPHTTPServer{}
	got, err = ResolveMCPBindings(proto.PromptRequestPayload{DisableExecutionEnvironment: true, MCPHTTPServers: &empty})
	if err != nil || got == nil || len(got) != 0 {
		t.Fatal("explicit empty profile lost ownership")
	}
}

func TestMCPBindingsRejectRelocationAndAmbiguousInstallation(t *testing.T) {
	public := []proto.MCPHTTPServer{{ConnectionOrigin: "service", ServerLabel: "remote", ServerURL: "https://example.test/mcp"}}
	local := &proto.LocalEnvironment{NetworkAccess: "enabled", MCP: []proto.EnvironmentMCP{{Server: agentplugin.MCPServer{Name: "remote", Type: "http", URL: "https://example.test/mcp"}}}}
	for _, req := range []proto.PromptRequestPayload{
		{MCPHTTPServers: &public, LocalEnvironment: local},
		{DisableExecutionEnvironment: true, LocalEnvironment: local},
		{LocalEnvironment: &proto.LocalEnvironment{NetworkAccess: "enabled", MCP: append(local.MCP, local.MCP[0])}},
		{LocalEnvironment: &proto.LocalEnvironment{NetworkAccess: "disabled", MCP: local.MCP}},
		{LocalEnvironment: &proto.LocalEnvironment{NetworkAccess: "enabled", MCP: []proto.EnvironmentMCP{{Server: agentplugin.MCPServer{Name: "remote", Type: "http", URL: "https://example.test/mcp", BearerTokenEnvVar: "MISSING"}}}}},
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
	req := proto.PromptRequestPayload{MCPHTTPServers: &public, LocalEnvironment: &proto.LocalEnvironment{NetworkAccess: "enabled"}}
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
	req.LocalEnvironment.MCP = []proto.EnvironmentMCP{{Server: agentplugin.MCPServer{Name: "remote", Type: "http", URL: "https://example.test/mcp"}}}
	if _, err := ResolveMCPBindings(req); err == nil {
		t.Fatal("public/Plugin identity collision accepted")
	}
	req.LocalEnvironment = nil
	if _, err := ResolveMCPBindings(req); err == nil {
		t.Fatal("unprepared environment accepted")
	}
}
