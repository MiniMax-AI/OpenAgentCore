package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// The Session, Vault, Credential, Template and event bodies follow the pinned
// request shapes: a field error names the offending field as param.
func TestRequestShapesReportFieldErrors(t *testing.T) {
	validation, _ := validationHandler(t)
	credentials, f, _ := credentialHandler(t)
	session := uuid.NewString()
	credentialPath := "/v1/vaults/" + f.credential.VaultID + "/credentials"
	const agent = `"agent":{"model":"m"},"environment":{"type":"none"}`
	for _, tc := range []struct {
		name         string
		handler      http.Handler
		path, body   string
		param, error string
	}{
		{"session unknown member", validation, "/v1/agents/sessions", `{` + agent + `,"x":1}`, "x", "Unknown parameter: 'x'."},
		{"session metadata", validation, "/v1/agents/sessions", `{` + agent + `,"metadata":[]}`, "metadata", "Invalid type for 'metadata': expected an object with string keys and string values, but got an array instead."},
		{"session role", validation, "/v1/agents/sessions", `{` + agent + `,"input":[{"role":"assistant","content":[]}]}`, "input[0].role", "Invalid value: 'assistant'. Supported values are: 'user'."},
		{"session content type", validation, "/v1/agents/sessions", `{` + agent + `,"input":[{"role":"user","content":[{"type":"input_audio"}]}]}`, "input[0].content[0].type", "Invalid value: 'input_audio'. Supported values are: 'input_text' and 'input_image'."},
		{"session update unknown member", validation, "/v1/agents/sessions/" + session, `{"x":1}`, "x", "Unknown parameter: 'x'."},
		{"session update metadata", validation, "/v1/agents/sessions/" + session, `{"metadata":1}`, "metadata", "Invalid type for 'metadata': expected an object with string keys and string values, but got an integer instead."},
		{"event role", validation, "/v1/agents/sessions/" + session + "/events", `{"events":[{"type":"agent.session.input.message","input":[{"role":"system","content":[]}]}]}`, "events[0].input[0].role", "Invalid value: 'system'. Supported values are: 'user'."},
		{"vault name", validation, "/v1/vaults", `{"name":1}`, "name", "Invalid type for 'name': expected a string, but got an integer instead."},
		{"vault unknown member", validation, "/v1/vaults", `{"foo":1}`, "foo", "Unknown parameter: 'foo'."},
		{"template name", validation, "/v1/agents/environments/templates", `{"name":1}`, "name", "Invalid type for 'name': expected a string, but got an integer instead."},
		{"template name length", validation, "/v1/agents/environments/templates", `{"name":""}`, "name", "name must contain 1 to 256 characters."},
		{"template env value", validation, "/v1/agents/environments/templates/" + uuid.NewString(), `{"env":{"KEY":1}}`, "env.KEY", "Invalid type for 'env.KEY': expected a string, but got an integer instead."},
		{"template env name", validation, "/v1/agents/environments/templates", `{"env":{"PATH":"x"}}`, "env.PATH", "Environment variable names must match [A-Za-z_][A-Za-z0-9_]* and must not be PATH, OPENAI_API_KEY or start with CODEX_ or OAC_; values must not contain U+0000."},
		{"credential missing token", credentials, credentialPath, `{"name":"n","auth":{"type":"static_bearer","mcp_server_url":"https://mcp.example/tools"}}`, "auth.token", "Missing required parameter: 'auth.token'."},
		{"credential auth type", credentials, credentialPath, `{"name":"n","auth":{"type":"basic"}}`, "auth.type", "Invalid value: 'basic'. Supported values are: 'mcp_oauth' and 'static_bearer'."},
		{"credential missing access token", credentials, credentialPath, `{"name":"n","auth":{"type":"mcp_oauth","mcp_server_url":"https://mcp.example/tools"}}`, "auth.access_token", "Missing required parameter: 'auth.access_token'."},
		{"credential url", credentials, credentialPath, `{"name":"n","auth":{"type":"static_bearer","mcp_server_url":"http://mcp.example","token":"t"}}`, "auth.mcp_server_url", "auth.mcp_server_url must be an absolute HTTPS URL without userinfo or a fragment."},
		{"credential empty token", credentials, credentialPath, `{"name":"n","auth":{"type":"static_bearer","mcp_server_url":"https://mcp.example","token":""}}`, "auth.token", "auth.token must be a nonempty string."},
		{"rotate unknown member", credentials, credentialPath + "/" + f.credential.ID, `{"auth":{"type":"static_bearer","token":"t","mcp_server_url":"https://mcp.example"}}`, "auth.mcp_server_url", "Unknown parameter: 'auth.mcp_server_url'."},
		{"rotate empty update", credentials, credentialPath + "/" + f.credential.ID, `{"auth":{"type":"mcp_oauth"}}`, "auth", "auth requires access_token, expires_at or refresh to update."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := credentialRequest(tc.handler, http.MethodPost, tc.path, tc.body)
			code, param, message := errorFields(t, w)
			if w.Code != http.StatusBadRequest || code != "invalid_request_error" || param == nil || *param != tc.param || message != tc.error {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
}
