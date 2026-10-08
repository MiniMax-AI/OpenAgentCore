package builtin

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// Each declared rule has a selection that one Harness admits and another
// rejects, or that a Runtime narrowing every capability rejects. Core and the
// Runtime admit selections through this one validator and these declarations.
func TestSelectionsAgainstEachDeclaration(t *testing.T) {
	text, blank, inline, remote := "inspect", " \t", "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aXioAAAAASUVORK5CYII=", "https://example.test/image.png"
	image := func(url string) []proto.InputContent {
		return []proto.InputContent{{Type: "input_image", ImageURL: &url}}
	}
	result := func(success bool, content []proto.InputContent) *proto.FunctionResultPayload {
		return &proto.FunctionResultPayload{CallID: "call", Success: success, Content: content}
	}
	tools, wildcard := []string{"search"}, []string{"*"}
	service := func(label string) []proto.SelectedMCP { return []proto.SelectedMCP{{Origin: "service", Label: label}} }
	environment := func(server proto.SelectedMCP) []proto.SelectedMCP {
		server.Origin = "environment"
		if server.Label == "" {
			server.Label = "docs"
		}
		return []proto.SelectedMCP{server}
	}
	schema, lossy := json.RawMessage(`{"type":"object","properties":{}}`), json.RawMessage(`{"type":"object","const":9007199254740993}`)
	search := proto.Selection{Environment: "none", Functions: true, DeferredFunctions: true, ToolSearch: true}
	with := func(s proto.Selection, change func(*proto.Selection)) proto.Selection { change(&s); return s }
	const tool, format, multi = "agent.tools", "agent.text.format", "agent.multi_agent"
	var none proto.AgentKindCapabilities
	for i, fields := 0, reflect.ValueOf(&none).Elem(); i < fields.NumField(); i++ {
		fields.Field(i).Set(reflect.ValueOf(proto.CapabilityUnsupported))
	}
	for _, c := range []struct {
		name       string
		selection  proto.Selection
		rejects    map[string]string // Harness to the rejection's param
		capability bool              // a Runtime that advertises no capability rejects it
	}{
		{"environment none", proto.Selection{Environment: "none"}, nil, true},
		{"local environment", proto.Selection{Environment: "local"}, nil, true},
		{"installed capabilities", proto.Selection{Environment: "local", InstalledCapabilities: true}, nil, true},
		{"multi-agent", proto.Selection{Environment: "none", MultiAgent: true}, nil, true},
		{"native session recovery", proto.Selection{Environment: "none", NativeSessionRecovery: true}, map[string]string{"claude_sdk": "", "mcode": ""}, true},
		{"function tools", proto.Selection{Environment: "none", Functions: true}, map[string]string{"mcode": tool}, true},
		{"tool search", search, map[string]string{"codex": tool, "mcode": tool}, true},
		{"json_schema output", proto.Selection{Environment: "none", OutputSchema: schema}, map[string]string{"codex": format, "mcode": format}, true},
		{"lossy json_schema output", proto.Selection{Environment: "none", OutputSchema: lossy}, map[string]string{"codex": format, "claude_sdk": format, "mcode": format}, false},
		{"medium text verbosity", proto.Selection{Environment: "none", TextVerbosity: "medium"}, nil, false},
		{"high text verbosity", proto.Selection{Environment: "none", TextVerbosity: "high"}, map[string]string{"claude_sdk": "agent.text.verbosity", "mcode": "agent.text.verbosity"}, true},
		{"text message", proto.Selection{Messages: proto.TextInput(text)}, nil, false},
		{"whitespace-only message", proto.Selection{Messages: proto.TextInput(blank)}, map[string]string{"claude_sdk": "", "mcode": ""}, false},
		{"message image", proto.Selection{Messages: proto.MessageInput{{Content: image(inline)}}}, map[string]string{"mcode": ""}, true},
		{"function result", proto.Selection{FunctionResult: result(true, []proto.InputContent{{Type: "input_text", Text: &text}})}, map[string]string{"mcode": ""}, true},
		{"function result image", proto.Selection{FunctionResult: result(true, image(inline))}, map[string]string{"mcode": ""}, true},
		{"function result image URL", proto.Selection{FunctionResult: result(true, image(remote))}, map[string]string{"claude_sdk": "", "mcode": ""}, false},
		{"failed function result image", proto.Selection{FunctionResult: result(false, image(inline))}, map[string]string{"claude_sdk": "", "mcode": ""}, false},
		{"service MCP", proto.Selection{Environment: "none", MCP: service("docs")}, map[string]string{"mcode": tool}, true},
		{"environment MCP", proto.Selection{Environment: "local", MCP: environment(proto.SelectedMCP{})}, nil, true},
		{"MCP allowlist", proto.Selection{Environment: "local", MCP: environment(proto.SelectedMCP{AllowedTools: &tools})}, map[string]string{"mcode": tool}, false},
		{"required MCP", proto.Selection{Environment: "local", MCP: environment(proto.SelectedMCP{Required: true})}, map[string]string{"mcode": tool}, true},
		{"authenticated MCP", proto.Selection{Environment: "local", MCP: environment(proto.SelectedMCP{Bearer: true})}, nil, true},
		{"MCP label codex_apps", proto.Selection{Environment: "local", MCP: environment(proto.SelectedMCP{Label: "codex_apps"})}, map[string]string{"codex": tool}, false},
		{"MCP label functions", proto.Selection{Environment: "local", MCP: environment(proto.SelectedMCP{Label: "functions"})}, map[string]string{"claude_sdk": tool}, false},
		{"MCP label oac_workspace", proto.Selection{Environment: "local", MCP: environment(proto.SelectedMCP{Label: "oac_workspace"})}, map[string]string{"mcode": tool}, false},
		{"MCP label outside the pattern", proto.Selection{Environment: "local", MCP: environment(proto.SelectedMCP{Label: "docs.v1"})}, map[string]string{"claude_sdk": tool}, false},
		{"MCP tool name outside the pattern", proto.Selection{Environment: "local", MCP: environment(proto.SelectedMCP{AllowedTools: &wildcard})}, map[string]string{"claude_sdk": tool, "mcode": tool}, false},
		{"installed MCP label", proto.Selection{Environment: "local", MCP: environment(proto.SelectedMCP{Label: "functions", Installed: true})}, map[string]string{"claude_sdk": "environment"}, false},
		{"multi-agent with installed MCP", proto.Selection{Environment: "local", MultiAgent: true, MCP: environment(proto.SelectedMCP{Installed: true})}, map[string]string{"claude_sdk": multi}, false},
		{"json_schema with multi-agent", proto.Selection{Environment: "none", OutputSchema: schema, MultiAgent: true}, map[string]string{"codex": format, "claude_sdk": format, "mcode": format}, false},
		{"json_schema with MCP", proto.Selection{Environment: "none", OutputSchema: schema, MCP: service("docs")}, map[string]string{"codex": format, "claude_sdk": format, "mcode": format}, false},
		{"json_schema with installed capabilities", proto.Selection{Environment: "local", InstalledCapabilities: true, OutputSchema: schema}, map[string]string{"codex": format, "claude_sdk": format, "mcode": format}, false},
		{"json_schema with tool search", with(search, func(s *proto.Selection) { s.OutputSchema = schema }), map[string]string{"codex": tool, "claude_sdk": format, "mcode": tool}, false},
		{"tool search with MCP", with(search, func(s *proto.Selection) { s.MCP = service("docs") }), map[string]string{"codex": tool, "claude_sdk": tool, "mcode": tool}, false},
		{"tool search with installed capabilities", with(search, func(s *proto.Selection) { s.Environment, s.InstalledCapabilities = "local", true }), map[string]string{"codex": tool, "claude_sdk": tool, "mcode": tool}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, kind := range Kinds() {
				declaration := Configuration(kind).Declaration
				err := proto.ValidateSelection(declaration, c.selection)
				param, rejected := c.rejects[kind]
				var selectionErr *proto.SelectionError
				if rejected != (err != nil) || rejected && (!errors.As(err, &selectionErr) || selectionErr.Param != param) {
					t.Errorf("%s: %v, want rejected=%v with param %q", kind, err, rejected, param)
				}
				if !c.capability || rejected {
					continue
				}
				narrowed, err := declaration.Narrow(none)
				if err != nil {
					t.Fatal(err)
				}
				if proto.ValidateSelection(narrowed, c.selection) == nil {
					t.Errorf("%s: a Runtime without the capability admitted it", kind)
				}
			}
		})
	}
}
