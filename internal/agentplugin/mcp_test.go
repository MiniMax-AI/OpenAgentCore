package agentplugin

import (
	"reflect"
	"testing"
)

func TestMCPOnlyPluginPreservesEnvironmentDeclarations(t *testing.T) {
	files := map[string][]byte{
		".codex-plugin/plugin.json": []byte(`{"name":"proof","description":"Use shared resources.","mcpServers":"./config/servers.json"}`),
		"config/servers.json": []byte(`{"mcpServers":{
			"remote":{"type":"http","url":"https://example.com/mcp","bearer_token_env_var":"PLUGIN_TOKEN","http_headers":{"X-Literal":"${DO_NOT_EXPAND}"}},
			"local":{"command":"python3","args":["server.py","--value","literal $VALUE"],"env_vars":["PLUGIN_VALUE"],"cwd":"./server"}
		}}`),
		"server/server.py": []byte("# Package resource, never imported by the parser.\n"),
	}
	t.Setenv("DO_NOT_EXPAND", "private-native-value")
	t.Setenv("PLUGIN_TOKEN", "must-not-resolve-here")
	bundle, err := Read(pluginArchive(t, files, 0600), Metadata{Type: "inline", Name: "proof", Description: "Use shared resources."})
	if err != nil || len(bundle.Skills) != 0 || len(bundle.MCP) != 2 {
		t.Fatalf("MCP-only bundle: %+v %v", bundle, err)
	}
	local, remote := bundle.MCP[0], bundle.MCP[1]
	if local.Name != "local" || local.Type != "stdio" || local.CWD != "./server" ||
		!reflect.DeepEqual(local.Args, []string{"server.py", "--value", "literal $VALUE"}) || !reflect.DeepEqual(local.EnvVars, []string{"PLUGIN_VALUE"}) ||
		remote.Name != "remote" || remote.BearerTokenEnvVar != "PLUGIN_TOKEN" || remote.HTTPHeaders["X-Literal"] != "${DO_NOT_EXPAND}" {
		t.Fatalf("declaration changed: %+v", bundle.MCP)
	}
}

func TestPluginCombinesSkillAndMCPWithoutActivatingOtherFiles(t *testing.T) {
	files := pluginFixture()
	files[".mcp.json"] = []byte(`{"mcpServers":{"remote":{"type":"http","url":"https://example.com/mcp"}}}`)
	files["hooks/hooks.json"] = []byte(`{"must_remain_inert":true}`)
	bundle, err := Read(pluginArchive(t, files, 0600), Metadata{Type: "inline", Name: "proof", Description: "Use shared resources."})
	if err != nil || len(bundle.Skills) != 2 || len(bundle.MCP) != 1 {
		t.Fatalf("combined package: %+v %v", bundle, err)
	}
}

func TestMCPRejectsUnsupportedAuthorityAndMalformedTransport(t *testing.T) {
	for _, input := range []string{
		`{"type":"http","url":"https://user:secret@example.com/mcp"}`,
		`{"type":"http","url":"file:///private"}`,
		`{"type":"http","url":"https://example.com/mcp","command":"sh"}`,
		`{"type":"http","url":"https://example.com/mcp","http_headers":{"X-Key":"injected\r\nheader"}}`,
		`{"type":"http","url":"https://example.com/mcp","http_headers":{"X-Key":"a","x-key":"b"}}`,
		`{"type":"http","url":"https://example.com/mcp","env_http_headers":{"X-Key":"SECRET"}}`,
		`{"type":"stdio","command":"python3","env":{"KEY":"inline value"}}`,
		`{"type":"stdio","command":"python3","env_vars":["BAD-NAME"]}`,
		`{"type":"stdio","command":"python3","cwd":"../private"}`,
		`{"type":"stdio","command":"python3","args":["bad\u0000argument"]}`,
		`{"type":"sse","url":"https://example.com"}`,
	} {
		files := pluginFixture()
		files[".mcp.json"] = []byte(`{"mcpServers":{"test":` + input + `}}`)
		if _, err := Read(pluginArchive(t, files, 0600), Metadata{Type: "inline", Name: "proof", Description: "Use shared resources."}); err == nil {
			t.Errorf("unsupported declaration accepted: %s", input)
		}
	}
}
