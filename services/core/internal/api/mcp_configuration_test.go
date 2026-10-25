package api

import (
	"fmt"

	"encoding/json"
	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

const publicMCP = `{"type":"mcp","server_label":"records","connection_origin":"service","transport":{"type":"http","server_url":"https://mcp.example.test/tools"}}`

func TestMCPRequiredSavedAndEffectiveConfiguration(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		input := strings.TrimSuffix(publicMCP, "}") + `,"required":` + value + `}`
		saved, err := resolveMCPTool(json.RawMessage(input), "tools[0]", true)
		if err != nil {
			t.Fatal(err)
		}
		effective, err := resolveSessionTools([]json.RawMessage{saved})
		if err != nil || len(effective) != 1 {
			t.Fatal("effective declaration failed", err)
		}
		for _, raw := range []json.RawMessage{saved, effective[0]} {
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) != nil || string(fields["required"]) != value {
				t.Fatal("required initialization changed", string(raw))
			}
		}
	}
}

func TestMCPResourceTransportProjections(t *testing.T) {
	for _, saved := range []bool{false, true} {
		raw, err := resolveMCPTool(json.RawMessage(publicMCP), "tools[0]", saved)
		if err != nil {
			t.Fatal(err)
		}
		var actual map[string]any
		if err := json.Unmarshal(raw, &actual); err != nil {
			t.Fatal(err)
		}
		transport := map[string]any{"type": "http", "server_url": "https://mcp.example.test/tools"}
		if saved {
			transport["headers"] = map[string]any{}
		}
		expected := map[string]any{"type": "mcp", "server_label": "records", "transport": transport,
			"connection_origin": "service", "required": false, "allowed_tools": nil,
			"credential_id": nil, "request_metadata": map[string]any{}}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("saved=%v: unexpected pinned MCP resource: %s", saved, raw)
		}
	}
}

// MV-01: an omitted or null origin on an HTTP server is stored exactly as an
// explicit "service" declaration, in saved Agents and Session configuration.
func TestMCPOmittedOriginIsService(t *testing.T) {
	for _, saved := range []bool{false, true} {
		explicit, err := resolveMCPTool(json.RawMessage(publicMCP), "tools[0]", saved)
		if err != nil {
			t.Fatal(err)
		}
		for _, origin := range []string{"", `,"connection_origin":null`} {
			input := `{"type":"mcp","server_label":"records"` + origin + `,"transport":{"type":"http","server_url":"https://mcp.example.test/tools"}}`
			resolved, err := resolveMCPTool(json.RawMessage(input), "tools[0]", saved)
			if err != nil || string(resolved) != string(explicit) {
				t.Fatalf("saved=%v origin %q: %s, %v; want %s", saved, origin, resolved, err, explicit)
			}
		}
	}
}

// A stdio server runs in the Environment: the origin defaults to it, an inline
// env is rejected, and the saved and effective forms match the pinned resource.
func TestMCPStdioDeclaration(t *testing.T) {
	const stdio = `{"type":"stdio","command":"run","args":["-x"],"cwd":"/work","env_vars":["TOKEN"]`
	input := func(origin, transport string) json.RawMessage {
		return json.RawMessage(`{"type":"mcp","server_label":"records"` + origin + `,"transport":` + transport + `}`)
	}
	for _, saved := range []bool{false, true} {
		for _, origin := range []string{"", `,"connection_origin":null`, `,"connection_origin":"environment"`} {
			raw, err := resolveMCPTool(input(origin, stdio+`,"env":{}}`), "tools[0]", saved)
			var tool map[string]any
			if err != nil || json.Unmarshal(raw, &tool) != nil || tool["connection_origin"] != "environment" ||
				!reflect.DeepEqual(tool["transport"], map[string]any{"type": "stdio", "command": "run", "args": []any{"-x"}, "cwd": "/work", "env_vars": []any{"TOKEN"}}) {
				t.Fatalf("saved=%v origin %q: %s, %v", saved, origin, raw, err)
			}
		}
	}
	raw, err := resolveMCPTool(input("", `{"type":"stdio","command":"run","args":null,"cwd":"/","env_vars":null}`), "tools[0]", true)
	if err != nil || !strings.Contains(string(raw), `"args":[]`) || !strings.Contains(string(raw), `"env_vars":[]`) {
		t.Fatalf("null lists: %s, %v", raw, err)
	}
	for message, raw := range map[string]json.RawMessage{
		"Inline MCP env is not supported; declare env_vars and set the values in the Environment.": input("", stdio+`,"env":{"TOKEN":"private-marker"}}`),
		"MCP stdio servers run in the Environment; connection_origin must be environment.":         input(`,"connection_origin":"service"`, stdio+`}`),
	} {
		if _, err := resolveMCPTool(raw, "tools[0]", false); err == nil || err.Error() != message {
			t.Fatalf("want %q, got %v", message, err)
		}
	}
	const invalid = "MCP stdio servers require a nonempty command, an absolute cwd, variable names in env_vars and no NUL characters."
	for _, transport := range []string{`{"type":"stdio","command":" ","cwd":"/"}`, `{"type":"stdio","command":"run","cwd":"work"}`,
		`{"type":"stdio","command":"run","cwd":"/","env_vars":["A-B"]}`, `{"type":"stdio","command":"run","args":["\u0000"],"cwd":"/"}`} {
		if _, err := resolveMCPTool(input("", transport), "tools[0]", true); err == nil || err.Error() != invalid {
			t.Fatalf("%s: %v", transport, err)
		}
	}
}

func TestMCPAllowedToolsAndOptionalFields(t *testing.T) {
	for _, allowed := range []string{"null", "[]", `["lookup","fail"]`} {
		var input map[string]json.RawMessage
		if err := json.Unmarshal([]byte(publicMCP), &input); err != nil {
			t.Fatal(err)
		}
		input["allowed_tools"] = json.RawMessage(allowed)
		input["credential_id"], input["request_metadata"], input["required"] = json.RawMessage("null"), json.RawMessage("null"), json.RawMessage("false")
		input["transport"] = json.RawMessage(`{"type":"http","server_url":"https://mcp.example.test/tools","authorization":null,"headers":null}`)
		encoded, _ := json.Marshal(input)
		resolved, err := resolveMCPTool(encoded, "tools[0]", false)
		if err != nil {
			t.Fatal(err)
		}
		var output map[string]json.RawMessage
		if err := json.Unmarshal(resolved, &output); err != nil || string(output["allowed_tools"]) != allowed {
			t.Fatalf("allow-list presence changed: %s, %v", resolved, err)
		}
	}
}

// The pinned shape is checked at decode; these inputs match it.
func TestMCPUnsupportedInputsAreSecretSafe(t *testing.T) {
	for name, replacement := range map[string]map[string]json.RawMessage{
		"empty credential":     {"credential_id": json.RawMessage(`""`)},
		"metadata":             {"request_metadata": json.RawMessage(`{"private-marker":"value"}`)},
		"inline authorization": {"transport": json.RawMessage(`{"type":"http","server_url":"https://mcp.example.test","authorization":"private-marker"}`)},
		"headers":              {"transport": json.RawMessage(`{"type":"http","server_url":"https://mcp.example.test","headers":{"Authorization":"private-marker"}}`)},
		"URL credentials":      {"transport": json.RawMessage(`{"type":"http","server_url":"https://private-marker@mcp.example.test"}`)},
		"header name":          {"transport": json.RawMessage(`{"type":"http","server_url":"https://mcp.example.test","headers":{"X Tenant":"private-marker"}}`)},
		"duplicate headers":    {"transport": json.RawMessage(`{"type":"http","server_url":"https://mcp.example.test","headers":{"X-A":"private-marker","x-a":"b"}}`)},
		"stdio credential":     {"credential_id": json.RawMessage(`"private-marker"`), "transport": json.RawMessage(`{"type":"stdio","command":"run","cwd":"/"}`)},
	} {
		t.Run(name, func(t *testing.T) {
			var input map[string]json.RawMessage
			if err := json.Unmarshal([]byte(publicMCP), &input); err != nil {
				t.Fatal(err)
			}
			for key, value := range replacement {
				if value == nil {
					delete(input, key)
				} else {
					input[key] = value
				}
			}
			encoded, _ := json.Marshal(input)
			if _, err := resolveMCPTool(encoded, "tools[0]", false); err == nil || strings.Contains(err.Error(), "private-marker") {
				t.Fatalf("unsupported MCP input was accepted or disclosed: %v", err)
			}
		})
	}
}

func TestMCPHeadersAreStoredAsGiven(t *testing.T) {
	for _, saved := range []bool{false, true} {
		input := `{"type":"mcp","server_label":"records","transport":{"type":"http","server_url":"https://mcp.example.test","headers":{"X-Tenant":"a"}}}`
		raw, err := resolveMCPTool(json.RawMessage(input), "tools[0]", saved)
		if err != nil || !strings.Contains(string(raw), `"headers":{"X-Tenant":"a"}`) {
			t.Fatalf("saved=%v: %s, %v", saved, raw, err)
		}
		if _, err := resolveMCPTool(json.RawMessage(strings.Replace(input, "X-Tenant", "authorization", 1)), "tools[0]", saved); err == nil {
			t.Fatal("Authorization header accepted")
		}
	}
}

func TestSessionMCPKeepsToolOrderAndStripsSavedHeaders(t *testing.T) {
	saved, err := resolveMCPTool(json.RawMessage(publicMCP), "tools[0]", true)
	if err != nil {
		t.Fatal(err)
	}
	function := json.RawMessage(`{"type":"function","name":"application","description":"An application callback","parameters":{"type":"object"},"defer_loading":false}`)
	tools, err := resolveSessionTools([]json.RawMessage{saved, function})
	if err != nil || len(tools) != 2 {
		t.Fatalf("mixed declaration: %v", err)
	}
	if strings.Contains(string(tools[0]), "headers") || string(tools[1]) != string(function) {
		t.Fatalf("Session transport or declared order changed: %s", tools)
	}
}

func TestSessionReadOmitsMCPHeaders(t *testing.T) {
	stored, err := resolveMCPTool(json.RawMessage(`{"type":"mcp","server_label":"records","transport":{"type":"http","server_url":"https://mcp.example.test","headers":{"X-Tenant":"a"}}}`), "tools[0]", false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(configuration{Agent: v1.Agent{ID: "agent", Model: "fixture", Tools: []json.RawMessage{stored}}, Environment: v1.Environment{Type: "none"}})
	response, err := sessionResponse(sessions.Session{Configuration: raw}, "")
	if err != nil || !strings.Contains(string(stored), "X-Tenant") {
		t.Fatal(err)
	}
	if got := string(response.Agent.Tools[0]); strings.Contains(got, "headers") || !strings.Contains(got, `"transport":{"type":"http","server_url":"https://mcp.example.test"}`) {
		t.Fatalf("Session read: %s", got)
	}
}

func TestPublicEnvironmentMCPPreservesDeclaration(t *testing.T) {
	for _, saved := range []bool{false, true} {
		for _, allowed := range []string{"null", "[]", `["prove"]`} {
			raw := []byte(`{"type":"mcp","server_label":"remote","connection_origin":"environment","transport":{"type":"http","server_url":"https://example.test/mcp"},"allowed_tools":` + allowed + `,"required":true,"credential_id":"selected"}`)
			resolved, err := resolveMCPTool(raw, "tools[0]", saved)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]json.RawMessage
			if json.Unmarshal(resolved, &got) != nil || string(got["connection_origin"]) != `"environment"` || string(got["allowed_tools"]) != allowed || string(got["credential_id"]) != `"selected"` || string(got["required"]) != "true" {
				t.Fatal("public declaration changed")
			}
		}
	}
}

func TestMCPServerURLKeepsQuery(t *testing.T) {
	const serverURL = "https://mcp.example.test/tools?tenant=a%20b"
	saved, err := resolveMCPTool(json.RawMessage(strings.Replace(publicMCP, "https://mcp.example.test/tools", serverURL, 1)), "tools[0]", true)
	if err != nil || !strings.Contains(string(saved), serverURL) {
		t.Fatalf("query URL was rejected or changed: %s, %v", saved, err)
	}
}

func TestFunctionsHaveNoCountOrNameLengthLimit(t *testing.T) {
	var input []v1.FunctionToolInput
	for i := 0; i < 65; i++ {
		name := fmt.Sprintf("f%d", i)
		if i == 0 {
			name = strings.Repeat("n", 600)
		}
		input = append(input, v1.FunctionToolInput{Name: name, Parameters: json.RawMessage(`{"type":"object"}`)})
	}
	if tools, err := resolveFunctions(input); err != nil || len(tools) != 65 {
		t.Fatal(len(tools), err)
	}
}
