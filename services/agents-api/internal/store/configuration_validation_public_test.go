package store_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

// Agent configuration protocol errors (TV-01..04) and repeated members reject
// before any write on every configuration path, with responses independent of
// resource ownership.
func TestAgentConfigurationValidationRejectsWithoutWritesPostgres(t *testing.T) {
	// An isolated database keeps the no-write digest independent of other tests.
	_, pool := store.NewManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{63}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := store.NewWithCredentialCipher(pool, cipher)
	owner, foreign, ownerTenant := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "config-owner", TokenSHA256: device.HashCredential(owner), TenantID: ownerTenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "config-foreign", TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err := api.NewHandler(s, auth, "codex", api.WithExecution(s))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	client := pathIDClient{t: t, server: server}
	const lookup = `{"type":"function","name":"lookup","description":"Look up a value.","parameters":{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}}`
	agent := client.created(owner, "/v1/agents", `{"model":"config-model","tools":[`+lookup+`]}`)
	// A record saved before the protocol checks existed.
	saved := func(tools string) string {
		return `{"model":"config-model","name":null,"instructions":null,"multi_agent":{"enabled":false,"max_concurrent_subagents":null},"reasoning":{},"service_tier":"auto","text":{"format":{"type":"text"},"verbosity":"medium"},"tools":` + tools + `}`
	}
	legacy, err := s.CreateAgent(t.Context(), ownerTenant, store.CreateAgentInput{Metadata: map[string]string{}, Configuration: json.RawMessage(saved(`[{"type":"function","name":"lookup","description":"","parameters":{"type":"string"},"defer_loading":false}]`))})
	if err != nil {
		t.Fatal(err)
	}
	before := databaseDigest(t, pool)

	const tools = `'function', 'tool_search', 'programmatic_tool_calling', 'mcp', and 'web_search'.`
	cases := []struct{ name, fields, param, message string }{
		{"F01", `"tools":[{"type":"function","name":"lookup","description":"Look up a value.","parameters":{"type":"string"}}]`, "", `Invalid schema for function 'lookup': schema must be a JSON Schema of 'type: "object"', got 'type: "string"'.`},
		{"F02", `"tools":[{"type":"function","name":"lookup","description":"Look up a value.","parameters":[]}]`, "{p}tools[0].parameters", "Invalid type for '{p}tools[0].parameters': expected an object with string keys and unknown value values, but got an array instead."},
		{"F03", `"tools":[{"type":"function","name":"lookup","description":"Look up a value."}]`, "{p}tools[0].parameters", "Missing required parameter: '{p}tools[0].parameters'."},
		{"F04", `"tools":[{"type":"function","name":"lookup","description":"Look up a value.","parameters":{"type":"object"},"strict":true}]`, "{p}tools[0].strict", "Unknown parameter: '{p}tools[0].strict'."},
		{"F05", `"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]`, "{p}tools[0].description", "Missing required parameter: '{p}tools[0].description'."},
		{"F08", `"tools":[` + lookup + `,{"type":"function","name":"lookup","description":"Second.","parameters":{"type":"object"}}]`, "", "duplicate function tool name: lookup"},
		{"F09", `"tools":["lookup"]`, "{p}tools[0]", "Invalid type for '{p}tools[0]': expected an object, but got a string instead."},
		{"F10", `"tools":[{"type":"function","name":"lookup","description":"","parameters":{},"defer_loading":"yes"}]`, "{p}tools[0].defer_loading", "Invalid type for '{p}tools[0].defer_loading': expected a boolean, but got a string instead."},
		{"U01", `"tools":[{"type":"code_interpreter"}]`, "{p}tools[0].type", "Invalid value: 'code_interpreter'. Supported values are: " + tools},
		{"U02", `"tools":[{"type":"bogus_tool"}]`, "{p}tools[0].type", "Invalid value: 'bogus_tool'. Supported values are: " + tools},
		{"W03", `"tools":[{"type":"web_search","mode":"bogus"}]`, "{p}tools[0].mode", "Invalid value: 'bogus'. Supported values are: 'disabled', 'cached', and 'live'."},
		{"W04", `"tools":[{"type":"web_search","mode":"disabled","context_size":"huge"}]`, "{p}tools[0].context_size", "Invalid value: 'huge'. Supported values are: 'low', 'medium', and 'high'."},
		{"W05", `"tools":[{"type":"web_search","mode":"disabled"},{"type":"web_search","mode":"disabled"}]`, "", "duplicate web_search tool"},
		{"P02", `"tools":[{"type":"programmatic_tool_calling","enabled":"yes"}]`, "{p}tools[0].enabled", "Invalid type for '{p}tools[0].enabled': expected a boolean, but got a string instead."},
		{"TS1", `"tools":[{"type":"tool_search","max_results":3}]`, "{p}tools[0].max_results", "Unknown parameter: '{p}tools[0].max_results'."},
		{"TS2", `"tools":[{"type":"tool_search"},{"type":"tool_search"}]`, "", "duplicate tool_search tool"},
		{"T01", `"text":{"format":{"type":"json_schema"}}`, "{p}text.format.schema", "Missing required parameter: '{p}text.format.schema'."},
		{"T02", `"text":{"format":{"type":"json_schema","name":"out","strict":true,"schema":{"type":"object","properties":{}}}}`, "{p}text.format.name", "Unknown parameter: '{p}text.format.name'."},
		{"T03", `"text":{"format":{"type":"json_object"}}`, "{p}text.format.type", "Invalid value: 'json_object'. Supported values are: 'text' and 'json_schema'."},
		{"T04", `"text":{"format":{"type":"json_schema","schema":{"type":"array","items":{"type":"string"}}}}`, "", `agent.text.format.schema must have top-level type "object"; got "array"`},
		{"R01", `"reasoning":{"effort":"extreme"}`, "{p}reasoning.effort", "Invalid value: 'extreme'. Supported values are: 'none', 'minimal', 'low', 'medium', 'high', 'xhigh', and 'max'."},
		{"R02", `"reasoning":{"summary":"verbose"}`, "{p}reasoning.summary", "Invalid value: 'verbose'. Supported values are: 'concise', 'detailed', and 'auto'."},
		{"S01", `"service_tier":"turbo"`, "{p}service_tier", "Invalid value: 'turbo'. Supported values are: 'auto', 'default', 'flex', 'priority', and 'fast'."},
		{"X01", `"tool_choice":"auto"`, "{p}tool_choice", "Unknown parameter: '{p}tool_choice'."},
		{"M01", `"multi_agent":{}`, "{p}multi_agent.enabled", "Missing required parameter: '{p}multi_agent.enabled'."},
		{"M02", `"multi_agent":{"enabled":true,"max_concurrent_subagents":0}`, "{p}multi_agent.max_concurrent_subagents", "Invalid '{p}multi_agent.max_concurrent_subagents': integer below minimum value. Expected a value >= 1, but got 0 instead."},
		// Repeated members would merge when decoded: the shared body gate rejects
		// them with a null param (HP-11). Member names match exactly, so a case
		// variant is an unknown member.
		{"merged text", `"text":{"format":{"type":"json_schema","schema":{"type":"array"}}},"text":{"verbosity":"low"}`, "", "Invalid body: duplicate JSON key 'text' at '{p}text'. Duplicate JSON keys are not supported."},
		{"merged reasoning", `"reasoning":{"Effort":"high"},"reasoning":{}`, "", "Invalid body: duplicate JSON key 'reasoning' at '{p}reasoning'. Duplicate JSON keys are not supported."},
		{"merged location", `"tools":[{"type":"web_search","mode":"disabled","location":{"city":"Paris"},"location":{"country":null}}]`, "", "Invalid body: duplicate JSON key 'location' at '{p}tools.location'. Duplicate JSON keys are not supported."},
		{"case variant", `"reasoning":{"effort":"high","EFFORT":"max"}`, "{p}reasoning.EFFORT", "Unknown parameter: '{p}reasoning.EFFORT'."},
	}
	expect := func(tc struct{ name, fields, param, message string }, prefix string) string {
		param := "null"
		if tc.param != "" {
			encoded, _ := json.Marshal(strings.ReplaceAll(tc.param, "{p}", prefix))
			param = string(encoded)
		}
		message, _ := json.Marshal(strings.ReplaceAll(tc.message, "{p}", prefix))
		return `{"error":{"message":` + string(message) + `,"type":"invalid_request_error","code":"invalid_request_error","param":` + param + `}}` + "\n"
	}
	for _, tc := range cases {
		saved, session := expect(tc, ""), expect(tc, "agent.")
		for _, request := range []struct{ token, path, body, want string }{
			{owner, "/v1/agents", `{"model":"m",` + tc.fields + `}`, saved},
			{foreign, "/v1/agents", `{"model":"m",` + tc.fields + `}`, saved},
			// Validation precedes the Agent lookup: owned, foreign, missing and malformed IDs agree.
			{owner, "/v1/agents/" + agent, `{` + tc.fields + `}`, saved},
			{foreign, "/v1/agents/" + agent, `{` + tc.fields + `}`, saved},
			{owner, "/v1/agents/" + uuid.NewString(), `{` + tc.fields + `}`, saved},
			{owner, "/v1/agents/not-an-agent", `{` + tc.fields + `}`, saved},
			{owner, "/v1/agents/sessions", `{"agent":{"model":"m",` + tc.fields + `},"environment":{"type":"none"},"input":"hi"}`, session},
			// C4: the inline configuration error precedes the input requirement.
			{owner, "/v1/agents/sessions", `{"agent":{"model":"m",` + tc.fields + `},"environment":{"type":"none"}}`, session},
			// An inline override is validated before the saved-Agent lookup.
			{owner, "/v1/agents/sessions", `{"agent_id":"` + agent + `","agent":{` + tc.fields + `},"environment":{"type":"none"},"input":"hi"}`, session},
			{foreign, "/v1/agents/sessions", `{"agent_id":"` + agent + `","agent":{` + tc.fields + `},"environment":{"type":"none"},"input":"hi"}`, session},
		} {
			if status, body := client.do(request.token, http.MethodPost, request.path, "application/json", []byte(request.body)); status != http.StatusBadRequest || body != request.want {
				t.Errorf("%s %s: %d %s", tc.name, request.path, status, body)
			}
		}
	}
	// A saved record from before the checks cannot execute; another tenant cannot see it.
	schema := `{"error":{"message":"Invalid schema for function 'lookup': schema must be a JSON Schema of 'type: \"object\"', got 'type: \"string\"'.","type":"invalid_request_error","code":"invalid_request_error","param":null}}` + "\n"
	if status, body := client.do(owner, http.MethodPost, "/v1/agents/sessions", "application/json", []byte(`{"agent_id":"`+legacy.ID+`","environment":{"type":"none"},"input":"hi"}`)); status != http.StatusBadRequest || body != schema {
		t.Errorf("legacy saved Agent: %d %s", status, body)
	}
	if status, body := client.do(foreign, http.MethodPost, "/v1/agents/sessions", "application/json", []byte(`{"agent_id":"`+legacy.ID+`","environment":{"type":"none"},"input":"hi"}`)); status != http.StatusNotFound {
		t.Errorf("foreign legacy saved Agent: %d %s", status, body)
	}
	// K2 keeps the local configuration code; saved enabled web_search (TV-05) is
	// covered by TestSavedWebSearchPostgres.
	for _, request := range []struct{ path, body, message string }{
		{"/v1/agents/sessions", `{"agent":{"model":"m","tools":[{"type":"web_search","mode":"live"}]},"environment":{"type":"none"},"input":"hi"}`, "Only disabled web_search is qualified for execution."},
		{"/v1/agents/sessions", `{"agent":{"model":"m","tools":[{"type":"web_search"}]},"environment":{"type":"none"},"input":"hi"}`, "Only disabled web_search is qualified for execution."},
		{"/v1/agents/sessions", `{"agent":{"model":"m","tools":[{"type":"programmatic_tool_calling","enabled":true}]},"environment":{"type":"none"},"input":"hi"}`, "Programmatic tool calling is not qualified for execution."},
		{"/v1/agents/sessions", `{"agent":{"model":"m","text":{"format":{"type":"json_schema","schema":{"type":"object"}}}},"environment":{"type":"none"},"input":"hi"}`, "Harness codex does not support the requested Agent/environment configuration: Structured output is not qualified for this engine."},
	} {
		encoded, _ := json.Marshal(request.message)
		want := `{"error":{"message":` + string(encoded) + `,"type":"invalid_request_error","code":"unsupported_or_invalid_configuration","param":null}}` + "\n"
		if status, body := client.do(owner, http.MethodPost, request.path, "application/json", []byte(request.body)); status != http.StatusBadRequest || body != want {
			t.Errorf("%s: %d %s", request.path, status, body)
		}
	}
	if after := databaseDigest(t, pool); !mapsEqual(before, after) {
		t.Fatal("rejected configuration changed persisted state")
	}

	// K1: values the official service accepts are saved and echoed unchanged.
	long := strings.Repeat("n", 65)
	update := `{"tools":[{"type":"function","name":"bad name!","description":"","parameters":{"type":"object"}},{"type":"function","name":"` + long + `","description":"","parameters":{}},{"type":"programmatic_tool_calling","enabled":true}],"reasoning":{"effort":"max"},"service_tier":"flex"}`
	status, body := client.do(owner, http.MethodPost, "/v1/agents/"+agent, "application/json", []byte(update))
	var updated struct {
		Tools       []map[string]any
		Reasoning   struct{ Effort string }
		ServiceTier string `json:"service_tier"`
	}
	if status != http.StatusOK || json.Unmarshal([]byte(body), &updated) != nil || len(updated.Tools) != 3 || updated.Tools[0]["name"] != "bad name!" || updated.Tools[1]["name"] != long ||
		updated.Tools[2]["enabled"] != true || updated.Reasoning.Effort != "max" || updated.ServiceTier != "flex" {
		t.Fatalf("K1 update: %d %s", status, body)
	}
	if status, retrieved := client.do(owner, http.MethodGet, "/v1/agents/"+agent, "", nil); status != http.StatusOK || retrieved != body {
		t.Fatalf("K1 retrieve: %d %s", status, retrieved)
	}
	// Another tenant can neither read nor change the Agent with a valid body.
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if status, _ := client.do(foreign, method, "/v1/agents/"+agent, "application/json", []byte(`{"service_tier":"auto"}`)); status != http.StatusNotFound {
			t.Fatalf("foreign %s: %d", method, status)
		}
	}
	if status, retrieved := client.do(owner, http.MethodGet, "/v1/agents/"+agent, "", nil); status != http.StatusOK || retrieved != body {
		t.Fatalf("foreign update changed the Agent: %d %s", status, retrieved)
	}
	// An inline Session with K1 function names executes on the none profile.
	client.created(owner, "/v1/agents/sessions", `{"agent":{"model":"m","tools":[{"type":"function","name":"bad name!","description":"","parameters":{"type":"object"}},{"type":"function","name":"`+long+`","description":"","parameters":{}}]},"environment":{"type":"none"},"input":"hi"}`)
}
