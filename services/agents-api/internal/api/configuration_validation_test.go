package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

// savedConfigurationStore serves fixed saved Agent configurations, including
// records saved before the protocol checks existed.
type savedConfigurationStore struct {
	*validationStore
	agents map[string]string
}

func (s *savedConfigurationStore) GetAgent(_ context.Context, tenant, id string) (store.SavedAgent, error) {
	configuration, ok := s.agents[id]
	if !ok {
		return store.SavedAgent{}, store.ErrNotFound
	}
	return store.SavedAgent{ID: id, TenantID: tenant, Configuration: json.RawMessage(configuration), Metadata: map[string]string{}}, nil
}

func configurationHandler(t *testing.T, agents map[string]string) (http.Handler, *savedConfigurationStore) {
	t.Helper()
	s := &savedConfigurationStore{validationStore: &validationStore{}, agents: agents}
	h, recording, _ := testHandler(t, WithExecution(&inputRecorder{ResourceStore: s}))
	recording.ResourceStore = s
	return h, s
}

type configurationOperation struct {
	name, path, prefix string
	body               func(fields string) string
}

func configurationOperations() []configurationOperation {
	return []configurationOperation{
		{"agent create", "/v1/agents", "", func(f string) string { return `{"model":"validation-model",` + f + `}` }},
		{"agent update", "/v1/agents/" + uuid.NewString(), "", func(f string) string { return `{` + f + `}` }},
		{"session create", "/v1/agents/sessions", "agent.", func(f string) string {
			return `{"agent":{"model":"validation-model",` + f + `},"environment":{"type":"none"},"input":"Validate."}`
		}},
		// C4: configuration errors precede the none input requirement.
		{"session create without input", "/v1/agents/sessions", "agent.", func(f string) string {
			return `{"agent":{"model":"validation-model",` + f + `},"environment":{"type":"none"}}`
		}},
	}
}

func lookupTool(name, fields string) string {
	return `{"type":"function","name":"` + name + `","description":"Look up a value.","parameters":{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}` + fields + `}`
}

func assertConfigurationError(t *testing.T, w *httptest.ResponseRecorder, code string, param *string, message string) {
	t.Helper()
	got, gotParam, gotMessage := errorFields(t, w)
	if w.Code != http.StatusBadRequest || got != code || (param == nil) != (gotParam == nil) || param != nil && *param != *gotParam || gotMessage != message {
		t.Fatalf("want %s %v %q, got %d %s", code, param, message, w.Code, w.Body)
	}
}

func TestAgentConfigurationProtocolErrorsUseOfficialFields(t *testing.T) {
	const tools = `, 'tool_search', 'programmatic_tool_calling', 'mcp', and 'web_search'.`
	cases := []struct{ name, fields, param, message string }{
		// C1, observed (TV-01). {p} is the Session "agent." prefix.
		{"F02", `"tools":[{"type":"function","name":"lookup","description":"Look up a value.","parameters":[]}]`, "{p}tools[0].parameters", "Invalid type for '{p}tools[0].parameters': expected an object with string keys and unknown value values, but got an array instead."},
		{"F03", `"tools":[{"type":"function","name":"lookup","description":"Look up a value."}]`, "{p}tools[0].parameters", "Missing required parameter: '{p}tools[0].parameters'."},
		{"F04", `"tools":[` + lookupTool("lookup", `,"strict":true`) + `]`, "{p}tools[0].strict", "Unknown parameter: '{p}tools[0].strict'."},
		{"F05", `"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]`, "{p}tools[0].description", "Missing required parameter: '{p}tools[0].description'."},
		{"U01", `"tools":[{"type":"code_interpreter"}]`, "{p}tools[0].type", "Invalid value: 'code_interpreter'. Supported values are: 'function'" + tools},
		{"U02", `"tools":[{"type":"bogus_tool"}]`, "{p}tools[0].type", "Invalid value: 'bogus_tool'. Supported values are: 'function'" + tools},
		{"W03", `"tools":[{"type":"web_search","mode":"bogus"}]`, "{p}tools[0].mode", "Invalid value: 'bogus'. Supported values are: 'disabled', 'cached', and 'live'."},
		{"W04", `"tools":[{"type":"web_search","mode":"disabled","context_size":"huge"}]`, "{p}tools[0].context_size", "Invalid value: 'huge'. Supported values are: 'low', 'medium', and 'high'."},
		{"P02", `"tools":[{"type":"programmatic_tool_calling","enabled":"yes"}]`, "{p}tools[0].enabled", "Invalid type for '{p}tools[0].enabled': expected a boolean, but got a string instead."},
		{"T01", `"text":{"format":{"type":"json_schema"}}`, "{p}text.format.schema", "Missing required parameter: '{p}text.format.schema'."},
		{"T02", `"text":{"format":{"type":"json_schema","name":"out","strict":true,"schema":{"type":"object","properties":{}}}}`, "{p}text.format.name", "Unknown parameter: '{p}text.format.name'."},
		{"T03", `"text":{"format":{"type":"json_object"}}`, "{p}text.format.type", "Invalid value: 'json_object'. Supported values are: 'text' and 'json_schema'."},
		{"R01", `"reasoning":{"effort":"extreme"}`, "{p}reasoning.effort", "Invalid value: 'extreme'. Supported values are: 'none', 'minimal', 'low', 'medium', 'high', 'xhigh', and 'max'."},
		{"R02", `"reasoning":{"summary":"verbose"}`, "{p}reasoning.summary", "Invalid value: 'verbose'. Supported values are: 'concise', 'detailed', and 'auto'."},
		{"S01", `"service_tier":"turbo"`, "{p}service_tier", "Invalid value: 'turbo'. Supported values are: 'auto', 'default', 'flex', 'priority', and 'fast'."},
		{"X01", `"tool_choice":"auto"`, "{p}tool_choice", "Unknown parameter: '{p}tool_choice'."},
		{"TS1", `"tools":[{"type":"tool_search","max_results":3}]`, "{p}tools[0].max_results", "Unknown parameter: '{p}tools[0].max_results'."},
		{"F09", `"tools":["lookup"]`, "{p}tools[0]", "Invalid type for '{p}tools[0]': expected an object, but got a string instead."},
		{"F10", `"tools":[` + lookupTool("lookup", `,"defer_loading":"yes"`) + `]`, "{p}tools[0].defer_loading", "Invalid type for '{p}tools[0].defer_loading': expected a boolean, but got a string instead."},
		{"M01", `"multi_agent":{}`, "{p}multi_agent.enabled", "Missing required parameter: '{p}multi_agent.enabled'."},
		{"M02", `"multi_agent":{"enabled":true,"max_concurrent_subagents":0}`, "{p}multi_agent.max_concurrent_subagents", "Invalid '{p}multi_agent.max_concurrent_subagents': integer below minimum value. Expected a value >= 1, but got 0 instead."},
		// C1, the same forms at unsampled positions of the pinned shapes.
		{"null tool", `"tools":[null]`, "{p}tools[0]", "Invalid type for '{p}tools[0]': expected an object, but got null instead."},
		{"missing tool type", `"tools":[{}]`, "{p}tools[0].type", "Missing required parameter: '{p}tools[0].type'."},
		{"null tool type", `"tools":[{"type":null}]`, "{p}tools[0].type", "Invalid type for '{p}tools[0].type': expected a string, but got null instead."},
		{"tools object", `"tools":{}`, "{p}tools", "Invalid type for '{p}tools': expected an array, but got an object instead."},
		{"second tool", `"tools":[{"type":"tool_search"},{"type":"web_search","mode":"disabled","location":{"extra":true}}]`, "{p}tools[1].location.extra", "Unknown parameter: '{p}tools[1].location.extra'."},
		{"null domain", `"tools":[{"type":"web_search","mode":"disabled","allowed_domains":[null]}]`, "{p}tools[0].allowed_domains[0]", "Invalid type for '{p}tools[0].allowed_domains[0]': expected a string, but got null instead."},
		{"null enabled", `"tools":[{"type":"programmatic_tool_calling","enabled":null}]`, "{p}tools[0].enabled", "Invalid type for '{p}tools[0].enabled': expected a boolean, but got null instead."},
		{"mcp origin", `"tools":[{"type":"mcp","server_label":"x","transport":{"type":"http","server_url":"https://example.invalid"},"connection_origin":"bogus"}]`, "{p}tools[0].connection_origin", "Invalid value: 'bogus'. Supported values are: 'service' and 'environment'."},
		{"mcp transport", `"tools":[{"type":"mcp","server_label":"x"}]`, "{p}tools[0].transport", "Missing required parameter: '{p}tools[0].transport'."},
		{"verbosity", `"text":{"verbosity":"verbose"}`, "{p}text.verbosity", "Invalid value: 'verbose'. Supported values are: 'low', 'medium', and 'high'."},
		{"text member", `"text":{"unknown":true}`, "{p}text.unknown", "Unknown parameter: '{p}text.unknown'."},
		{"format type", `"text":{"format":{}}`, "{p}text.format.type", "Missing required parameter: '{p}text.format.type'."},
		{"text schema", `"text":{"format":{"type":"text","schema":{}}}`, "{p}text.format.schema", "Unknown parameter: '{p}text.format.schema'."},
		{"schema array", `"text":{"format":{"type":"json_schema","schema":[]}}`, "{p}text.format.schema", "Invalid type for '{p}text.format.schema': expected an object with string keys and unknown value values, but got an array instead."},
		{"reasoning array", `"reasoning":[]`, "{p}reasoning", "Invalid type for '{p}reasoning': expected an object, but got an array instead."},
		{"case variant", `"reasoning":{"Effort":"high"}`, "{p}reasoning.Effort", "Unknown parameter: '{p}reasoning.Effort'."},
		// encoding/json merges repeated objects and matches names case-insensitively.
		// The shared body gate rejects repeated keys anywhere in the tree with a null
		// param (HP-11); case variants are unknown members.
		{"merged text", `"text":{"format":{"type":"json_schema","schema":{"type":"array"}}},"text":{"verbosity":"low"}`, "", "Invalid body: duplicate JSON key 'text' at '{p}text'. Duplicate JSON keys are not supported."},
		{"merged reasoning", `"reasoning":{"Effort":"high"},"reasoning":{}`, "", "Invalid body: duplicate JSON key 'reasoning' at '{p}reasoning'. Duplicate JSON keys are not supported."},
		{"merged location", `"tools":[{"type":"web_search","mode":"disabled","location":{"city":"Paris"},"location":{"country":null}}]`, "", "Invalid body: duplicate JSON key 'location' at '{p}tools.location'. Duplicate JSON keys are not supported."},
		{"case variant member", `"reasoning":{"effort":"high","EFFORT":"max"}`, "{p}reasoning.EFFORT", "Unknown parameter: '{p}reasoning.EFFORT'."},
		{"repeated scalar", `"reasoning":{"effort":"high","effort":"bogus"}`, "", "Invalid body: duplicate JSON key 'effort' at '{p}reasoning.effort'. Duplicate JSON keys are not supported."},
		{"repeated root", `"service_tier":"auto","service_tier":"flex"`, "", "Invalid body: duplicate JSON key 'service_tier' at '{p}service_tier'. Duplicate JSON keys are not supported."},
		{"repeated tool type", `"tools":[{"type":"function","type":"function","name":"f","description":"","parameters":{}}]`, "", "Invalid body: duplicate JSON key 'type' at '{p}tools.type'. Duplicate JSON keys are not supported."},
		{"repeated nested", `"multi_agent":{"enabled":true,"enabled":false}`, "", "Invalid body: duplicate JSON key 'enabled' at '{p}multi_agent.enabled'. Duplicate JSON keys are not supported."},
		{"case variant root", `"Text":{}`, "{p}Text", "Unknown parameter: '{p}Text'."},
		{"case variant nested", `"text":{"Verbosity":"low"}`, "{p}text.Verbosity", "Unknown parameter: '{p}text.Verbosity'."},
		{"case variant tool", `"tools":[{"type":"web_search","mode":"disabled","Location":{}}]`, "{p}tools[0].Location", "Unknown parameter: '{p}tools[0].Location'."},
		// The body gate reports a repeated key before any member or value check.
		{"unknown first", `"tool_choice":"auto","text":{},"text":{}`, "", "Invalid body: duplicate JSON key 'text' at '{p}text'. Duplicate JSON keys are not supported."},
		{"repeat first", `"text":{},"text":{},"tool_choice":"auto"`, "", "Invalid body: duplicate JSON key 'text' at '{p}text'. Duplicate JSON keys are not supported."},
		{"repeat before values", `"reasoning":{"effort":"bogus"},"text":{},"text":{}`, "", "Invalid body: duplicate JSON key 'text' at '{p}text'. Duplicate JSON keys are not supported."},
		{"enabled null", `"multi_agent":{"enabled":null}`, "{p}multi_agent.enabled", "Invalid type for '{p}multi_agent.enabled': expected a boolean, but got null instead."},
		{"maximum number", `"multi_agent":{"enabled":true,"max_concurrent_subagents":1.5}`, "{p}multi_agent.max_concurrent_subagents", "Invalid type for '{p}multi_agent.max_concurrent_subagents': expected an integer, but got a number instead."},
		{"maximum negative", `"multi_agent":{"enabled":true,"max_concurrent_subagents":-99999999999999999999}`, "{p}multi_agent.max_concurrent_subagents", "Invalid '{p}multi_agent.max_concurrent_subagents': integer below minimum value. Expected a value >= 1."},
		{"instructions type", `"instructions":false`, "{p}instructions", "Invalid type for '{p}instructions': expected a string, but got a boolean instead."},
		// C2 (TV-02) and C3 (TV-03): param null.
		{"F08", `"tools":[` + lookupTool("lookup", "") + `,{"type":"function","name":"lookup","description":"Second.","parameters":{"type":"object"}}]`, "", "duplicate function tool name: lookup"},
		{"W05", `"tools":[{"type":"web_search","mode":"disabled"},{"type":"web_search","mode":"disabled"}]`, "", "duplicate web_search tool"},
		{"TS2", `"tools":[{"type":"tool_search"},{"type":"tool_search"}]`, "", "duplicate tool_search tool"},
		{"F01", `"tools":[{"type":"function","name":"lookup","description":"Look up a value.","parameters":{"type":"string"}}]`, "", `Invalid schema for function 'lookup': schema must be a JSON Schema of 'type: "object"', got 'type: "string"'.`},
		{"T04", `"text":{"format":{"type":"json_schema","schema":{"type":"array","items":{"type":"string"}}}}`, "", `agent.text.format.schema must have top-level type "object"; got "array"`},
		{"root after duplicates", `"tools":[{"type":"tool_search"},` + lookupTool("a", "") + `,{"type":"function","name":"b","description":"","parameters":{"type":"null"}}]`, "", `Invalid schema for function 'b': schema must be a JSON Schema of 'type: "object"', got 'type: "null"'.`},
		// Structure is checked before conflicts.
		{"structure first", `"tools":[{"type":"tool_search"},{"type":"tool_search"},{"type":"bogus"}]`, "{p}tools[2].type", "Invalid value: 'bogus'. Supported values are: 'function'" + tools},
	}
	for _, op := range configurationOperations() {
		t.Run(op.name, func(t *testing.T) {
			h, s := configurationHandler(t, nil)
			for _, tc := range cases {
				w := credentialRequest(h, http.MethodPost, op.path, op.body(tc.fields))
				var param *string
				if tc.param != "" {
					value := strings.ReplaceAll(tc.param, "{p}", op.prefix)
					param = &value
				}
				t.Run(tc.name, func(t *testing.T) {
					assertConfigurationError(t, w, "invalid_request_error", param, strings.ReplaceAll(tc.message, "{p}", op.prefix))
				})
			}
			if s.writes != 0 {
				t.Fatalf("rejected configuration reached storage: %d writes", s.writes)
			}
		})
	}
}

func TestSessionAgentProtocolErrors(t *testing.T) {
	h, s := configurationHandler(t, nil)
	param := func(value string) *string { return &value }
	for _, tc := range []struct {
		body, param, message string
	}{
		{`{"agent":null,"environment":{"type":"none"},"input":"x"}`, "agent", "Invalid type for 'agent': expected an object, but got null instead."},
		{`{"agent":[],"environment":{"type":"none"}}`, "agent", "Invalid type for 'agent': expected an object, but got an array instead."},
		{`{"agent":{"model":"m","name":"x"},"environment":{"type":"none"}}`, "agent.name", "Unknown parameter: 'agent.name'."},
		{`{"agent":{"model":"m","metadata":{}},"environment":{"type":"none"}}`, "agent.metadata", "Unknown parameter: 'agent.metadata'."},
		{`{"agent":{"model":null},"environment":{"type":"none"}}`, "agent.model", "Invalid type for 'agent.model': expected a string, but got null instead."},
		{`{"agent":{"model":4},"environment":{"type":"none"}}`, "agent.model", "Invalid type for 'agent.model': expected a string, but got an integer instead."},
		// The body gate reports the first repeated key in document order.
		{`{"agent":{"model":"m"},"agent":{"model":"m","model":"n"},"environment":{"type":"none"}}`, "", "Invalid body: duplicate JSON key 'agent' at 'agent'. Duplicate JSON keys are not supported."},
	} {
		want := param(tc.param)
		if tc.param == "" {
			want = nil
		}
		assertConfigurationError(t, credentialRequest(h, http.MethodPost, "/v1/agents/sessions", tc.body), "invalid_request_error", want, tc.message)
	}
	for _, path := range []string{"/v1/agents", "/v1/agents/" + uuid.NewString()} {
		assertConfigurationError(t, credentialRequest(h, http.MethodPost, path, `{"model":4}`), "invalid_request_error", param("model"), "Invalid type for 'model': expected a string, but got an integer instead.")
		assertConfigurationError(t, credentialRequest(h, http.MethodPost, path, `{"model":"m","model":"n"}`), "invalid_request_error", nil, "Invalid body: duplicate JSON key 'model' at 'model'. Duplicate JSON keys are not supported.")
	}
	// Saved Agent creation requires a model; updates and Session overrides do not.
	assertConfigurationError(t, credentialRequest(h, http.MethodPost, "/v1/agents", `{"name":"x"}`), "invalid_request_error", param("model"), "Missing required parameter: 'model'.")
	if s.writes != 0 {
		t.Fatal("rejected configuration reached storage")
	}
}

// K1 and other values the official service accepts stay accepted, and schemas
// without a string root type are unchanged.
func TestAgentConfigurationAcceptedValuesUnchanged(t *testing.T) {
	long := strings.Repeat("n", 65)
	common := []string{
		`"tools":[` + lookupTool("bad name!", "") + `]`,
		`"tools":[` + lookupTool(long, "") + `]`,
		`"tools":[` + lookupTool("a", "") + `,` + lookupTool("b", `,"defer_loading":false`) + `]`,
		`"tools":[{"type":"function","name":"x","description":"","parameters":{}}]`,
		`"tools":[{"type":"function","name":"x","description":"","parameters":{"type":["object","null"]}}]`,
		`"tools":[{"type":"function","name":"x","description":"","parameters":{"type":"object","Type":"string"}}]`,
		`"tools":[{"type":"web_search","mode":"disabled","context_size":null,"allowed_domains":[],"location":{"city":null}},{"type":"programmatic_tool_calling","enabled":false}]`,
		`"tools":null`, `"text":{"format":null,"verbosity":null}`, `"reasoning":null`, `"multi_agent":{"enabled":false,"max_concurrent_subagents":4}`,
		`"service_tier":"auto"`, `"instructions":null`,
	}
	saved := append([]string{
		`"tools":[{"type":"programmatic_tool_calling","enabled":true}]`,
		// TV-05: every pinned web_search mode is saved; admission still qualifies only disabled.
		`"tools":[{"type":"web_search"}]`, `"tools":[{"type":"web_search","mode":null}]`,
		`"tools":[{"type":"web_search","mode":"live","allowed_domains":[]}]`,
		`"tools":[{"type":"web_search","mode":"cached","context_size":"high","allowed_domains":["example.com"],"location":{"country":"FR","city":"Paris"}}]`,
		`"reasoning":{"effort":"max","summary":"auto"}`, `"service_tier":"flex"`,
		`"text":{"format":{"type":"json_schema","schema":{"type":"object"}}}`,
		`"text":{"format":{"type":"json_schema","schema":{"properties":{}}}}`,
		`"tools":[{"type":"tool_search"},` + lookupTool("lookup", `,"defer_loading":true`) + `]`,
		`"multi_agent":{"enabled":true,"max_concurrent_subagents":4294967295}`,
		`"tools":[{"type":"mcp","server_label":"x","transport":{"type":"http","server_url":"https://example.invalid/mcp"},"connection_origin":"service","allowed_tools":null,"credential_id":null,"request_metadata":{},"required":false}]`,
	}, common...)
	for _, op := range configurationOperations()[:3] {
		h, s := configurationHandler(t, nil)
		accepted := common
		if op.prefix == "" {
			accepted = saved
		}
		for _, fields := range accepted {
			before := s.writes
			if w := credentialRequest(h, http.MethodPost, op.path, op.body(fields)); w.Code >= 300 || s.writes != before+1 {
				t.Fatalf("%s rejected %s: %d %s", op.name, fields, w.Code, w.Body)
			}
		}
	}
}

// K2: execution admission limits keep their codes. Enabled or omitted-mode
// web_search is saved (TV-05) but still rejected at Session admission.
func TestAgentConfigurationLocalLimitsKeepCodes(t *testing.T) {
	h, s := configurationHandler(t, nil)
	session := configurationOperations()[2]
	for _, tc := range []struct{ fields, message string }{
		{`"tools":[{"type":"web_search","mode":"live"}]`, "Only disabled web_search is qualified for execution."},
		{`"tools":[{"type":"web_search","mode":"cached"}]`, "Only disabled web_search is qualified for execution."},
		{`"tools":[{"type":"web_search","mode":null}]`, "Only disabled web_search is qualified for execution."},
		{`"tools":[{"type":"web_search"}]`, "Only disabled web_search is qualified for execution."},
		{`"tools":[{"type":"programmatic_tool_calling","enabled":true}]`, "Programmatic tool calling is not qualified for execution."},
		{`"tools":[{"type":"programmatic_tool_calling","enabled":false},{"type":"programmatic_tool_calling","enabled":false}]`, "Execution requires distinct tool controls."},
		{`"text":{"format":{"type":"json_schema","schema":{"type":"object"}}}`, "Harness codex does not support the requested Agent/environment configuration: Structured output is not qualified for this engine."},
		{`"reasoning":{"effort":"max"}`, "Explicit reasoning execution options are not supported by this service yet."},
		{`"service_tier":"flex"`, "Execution currently supports service_tier=auto only."},
		{`"tools":[{"type":"function","name":"","description":"","parameters":{}}]`, "Function names must be nonempty, unique and at most 512 bytes."},
	} {
		assertConfigurationError(t, credentialRequest(h, http.MethodPost, session.path, session.body(tc.fields)), "unsupported_or_invalid_configuration", nil, tc.message)
	}
	for _, op := range configurationOperations()[:2] {
		assertConfigurationError(t, credentialRequest(h, http.MethodPost, op.path, op.body(`"multi_agent":{"enabled":true,"max_concurrent_subagents":4294967296}`)), "unsupported_or_invalid_configuration", nil, "max_concurrent_subagents must be an integer from 1 to 4294967295.")
	}
	if s.writes != 0 {
		t.Fatal("rejected configuration reached storage")
	}
}

// C4 and SES-33: only configuration errors precede the input requirement;
// accepted configurations and saved-Agent lookup keep their order.
func TestSessionConfigurationValidationOrder(t *testing.T) {
	legacy := uuid.NewString()
	h, s := configurationHandler(t, map[string]string{legacy: `{"model":"m","tools":[{"type":"web_search","mode":"disabled"}]}`})
	const input = "conversation-only sessions currently require initial input"
	for _, body := range []string{
		`{"agent":{"model":"m","tools":[{"type":"web_search","mode":"live"}]},"environment":{"type":"none"}}`,
		`{"agent":{"model":"m","tools":[{"type":"programmatic_tool_calling","enabled":true}]},"environment":{"type":"none"}}`,
		`{"agent":{"model":"m","reasoning":{"effort":"max"}},"environment":{"type":"none"}}`,
		`{"agent":{"instructions":"x"},"environment":{"type":"none"}}`,
		`{"agent_id":"` + uuid.NewString() + `","environment":{"type":"none"}}`,
		`{"agent_id":"` + legacy + `","agent":{"tools":[{"type":"tool_search"}]},"environment":{"type":"none"}}`,
	} {
		assertConfigurationError(t, credentialRequest(h, http.MethodPost, "/v1/agents/sessions", body), "invalid_request_error", nil, input)
	}
	missing := uuid.NewString()
	if w := credentialRequest(h, http.MethodPost, "/v1/agents/sessions", `{"agent_id":"`+missing+`","agent":{"tools":[]},"environment":{"type":"none"},"input":"x"}`); w.Code != http.StatusNotFound {
		t.Fatal(w.Code, w.Body)
	}
	// An invalid inline override is reported before the saved-Agent lookup.
	for _, id := range []string{missing, legacy} {
		for _, suffix := range []string{``, `,"input":"x"`} {
			w := credentialRequest(h, http.MethodPost, "/v1/agents/sessions", `{"agent_id":"`+id+`","agent":{"tools":[{"type":"tool_search"},{"type":"tool_search"}]},"environment":{"type":"none"}`+suffix+`}`)
			assertConfigurationError(t, w, "invalid_request_error", nil, "duplicate tool_search tool")
		}
	}
	if s.writes != 0 {
		t.Fatal("rejected configuration reached storage")
	}
}

// Saved records that predate the conflict checks cannot execute.
func TestSessionAdmissionRejectsSavedConfigurationConflicts(t *testing.T) {
	saved := func(fields string) string {
		return `{"model":"m","multi_agent":{"enabled":false,"max_concurrent_subagents":null},"reasoning":{},"service_tier":"auto","text":{"format":{"type":"text"},"verbosity":"medium"},` + fields + `}`
	}
	records := map[string]struct{ configuration, message string }{
		uuid.NewString(): {saved(`"tools":[{"type":"function","name":"lookup","description":"","parameters":{"type":"string"},"defer_loading":false}]`), `Invalid schema for function 'lookup': schema must be a JSON Schema of 'type: "object"', got 'type: "string"'.`},
		uuid.NewString(): {saved(`"tools":[{"type":"function","name":"f","description":"","parameters":{},"defer_loading":false},{"type":"function","name":"f","description":"","parameters":{},"defer_loading":false}]`), "duplicate function tool name: f"},
		uuid.NewString(): {saved(`"tools":[{"type":"tool_search"},{"type":"tool_search"}]`), "duplicate tool_search tool"},
		uuid.NewString(): {saved(`"tools":[],"text":{"format":{"type":"json_schema","schema":{"type":"array"}},"verbosity":"medium"}`), `agent.text.format.schema must have top-level type "object"; got "array"`},
	}
	agents := map[string]string{}
	for id, record := range records {
		agents[id] = record.configuration
	}
	valid := uuid.NewString()
	agents[valid] = saved(`"tools":[{"type":"function","name":"f","description":"","parameters":{"type":"object"},"defer_loading":false}]`)
	h, s := configurationHandler(t, agents)
	for id, record := range records {
		w := credentialRequest(h, http.MethodPost, "/v1/agents/sessions", `{"agent_id":"`+id+`","environment":{"type":"none"},"input":"x"}`)
		assertConfigurationError(t, w, "invalid_request_error", nil, record.message)
	}
	if s.writes != 0 {
		t.Fatal("rejected configuration reached storage")
	}
	// A replacement of the conflicting field admits the saved record.
	for id, record := range records {
		field := `"tools":[]`
		if strings.Contains(record.configuration, "json_schema") {
			field = `"text":{"format":{"type":"text"}}`
		}
		if w := credentialRequest(h, http.MethodPost, "/v1/agents/sessions", `{"agent_id":"`+id+`","agent":{`+field+`},"environment":{"type":"none"},"input":"x"}`); w.Code != http.StatusCreated {
			t.Fatal(id, w.Code, w.Body)
		}
	}
	if w := credentialRequest(h, http.MethodPost, "/v1/agents/sessions", `{"agent_id":"`+valid+`","environment":{"type":"none"},"input":"x"}`); w.Code != http.StatusCreated {
		t.Fatal(w.Code, w.Body)
	}
}

// Caller-supplied names and values are repeated only when short and printable
// (the Environment Files rule), so every response stays small.
func TestAgentConfigurationErrorsBoundEchoedInput(t *testing.T) {
	long := strings.Repeat("<", 257)
	param := func(value string) *string { return &value }
	for _, op := range configurationOperations() {
		h, s := configurationHandler(t, nil)
		p := op.prefix
		for _, tc := range []struct {
			fields  string
			param   *string
			message string
		}{
			{`"` + long + `":1`, nil, "Unknown parameter."},
			{`"tools":[{"type":"tool_search","tab\tkey":1}]`, nil, "Unknown parameter."},
			{`"tools":[{"type":"tool_search","\ufffd":1}]`, nil, "Unknown parameter."},
			{`"tools":[{"type":"tool_search","` + strings.Repeat("<>", 64) + `":1}]`, param(p + "tools[0]." + strings.Repeat("<>", 64)), "Unknown parameter: '" + p + "tools[0]." + strings.Repeat("<>", 64) + "'."},
			{`"reasoning":{"` + strings.Repeat("a", 4<<10) + `":1}`, nil, "Unknown parameter."},
			{`"service_tier":"` + long + `"`, param(p + "service_tier"), "Invalid value. Supported values are: 'auto', 'default', 'flex', 'priority', and 'fast'."},
			{`"tools":[{"type":"line\u2028break"}]`, param(p + "tools[0].type"), "Invalid value. Supported values are: 'function', 'tool_search', 'programmatic_tool_calling', 'mcp', and 'web_search'."},
			{`"tools":[` + lookupTool(long, "") + `,` + lookupTool(long, "") + `]`, nil, "duplicate function tool name"},
			{`"tools":[{"type":"function","name":"` + long + `","description":"","parameters":{"type":"string"}}]`, nil, `Invalid schema for function: schema must be a JSON Schema of 'type: "object"', got 'type: "string"'.`},
			{`"tools":[{"type":"function","name":"lookup","description":"","parameters":{"type":"` + long + `"}}]`, nil, `Invalid schema for function 'lookup': schema must be a JSON Schema of 'type: "object"'.`},
			{`"text":{"format":{"type":"json_schema","schema":{"type":"` + long + `"}}}`, nil, `agent.text.format.schema must have top-level type "object"`},
		} {
			w := credentialRequest(h, http.MethodPost, op.path, op.body(tc.fields))
			if w.Body.Len() > 4096 {
				t.Fatalf("%s: unbounded response of %d bytes", op.name, w.Body.Len())
			}
			assertConfigurationError(t, w, "invalid_request_error", tc.param, tc.message)
		}
		if s.writes != 0 {
			t.Fatal("rejected configuration reached storage")
		}
	}
}
