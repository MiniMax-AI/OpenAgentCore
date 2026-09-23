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

// Every Agents API JSON route checks its body in the shared gate before any
// lookup or write (HP-09..HP-15). Bodies that would otherwise write, such as an
// invalid UTF-8 name stored as U+FFFD, a last-value-wins duplicate or an update
// sent as text/plain, change nothing for the owner or another tenant.
func TestRequestBodyGateRejectsWithoutWritesPostgres(t *testing.T) {
	// An isolated database keeps the no-write digest independent of other tests.
	_, pool := store.NewManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{64}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := store.NewWithCredentialCipher(pool, cipher)
	owner, foreign, ownerTenant := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "body-owner", TokenSHA256: device.HashCredential(owner), TenantID: ownerTenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "body-foreign", TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()},
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

	agent := client.created(owner, "/v1/agents", `{"model":"body-model","name":"body-agent","metadata":{"k":"v"}}`)
	vault := client.created(owner, "/v1/vaults", `{"name":"body-vault"}`)
	credential := client.created(owner, "/v1/vaults/"+vault+"/credentials", `{"name":"body","auth":{"type":"static_bearer","mcp_server_url":"https://mcp.example/mcp","token":"body-token"}}`)
	template := client.created(owner, "/v1/agents/environments/templates", `{"name":"body-template"}`)
	session := client.created(owner, "/v1/agents/sessions", `{"agent":{"model":"body-model"},"environment":{"type":"none"},"input":"Keep this Session.","metadata":{"k":"v"}}`)
	prepared, err := s.CreateSession(t.Context(), ownerTenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "body-environment",
		Configuration: json.RawMessage(`{"agent":{"model":"body-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace","capability_directories":[]}}`)})
	if err != nil || prepared.Environment == nil {
		t.Fatal("fixture Environment", err)
	}
	before := databaseDigest(t, pool)

	// Each valid body would write; the single "gate" string sits at the
	// dotted object-key path at.
	routes := []struct{ path, body, at string }{
		{"/v1/agents", `{"model":"m","name":"gate"}`, "name"},
		{"/v1/agents/" + agent, `{"metadata":{"k":"gate"}}`, "metadata.k"},
		{"/v1/vaults", `{"name":"gate"}`, "name"},
		{"/v1/vaults/" + vault + "/credentials", `{"name":"body","auth":{"type":"static_bearer","mcp_server_url":"https://mcp.example/mcp","token":"gate"}}`, "auth.token"},
		{"/v1/vaults/" + vault + "/credentials/" + credential, `{"auth":{"type":"static_bearer","token":"gate"}}`, "auth.token"},
		{"/v1/agents/environments/templates", `{"name":"gate"}`, "name"},
		{"/v1/agents/environments/templates/" + template, `{"name":"gate"}`, "name"},
		{"/v1/agents/environments/" + prepared.Environment.ID + "/files", `{"type":"inline","path":"/workspace/body","data":"gate"}`, "data"},
		{"/v1/agents/sessions", `{"agent":{"model":"m"},"environment":{"type":"none"},"input":"gate"}`, "input"},
		{"/v1/agents/sessions/" + session, `{"metadata":{"k":"gate"}}`, "metadata.k"},
		{"/v1/agents/sessions/" + session + "/events", `{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"gate"}]}]}]}`, "events.input.content.text"},
	}
	official := func(message string) string {
		encoded, _ := json.Marshal(message)
		return `{"error":{"message":` + string(encoded) + `,"type":"invalid_request_error","code":"invalid_request_error","param":null}}` + "\n"
	}
	parse := official("Invalid body: failed to parse JSON value. Please check the value to ensure it is valid JSON. (Common errors include trailing commas, missing closing brackets, missing quotation marks, etc.)")
	contentType := official("expected request with Content-Type: application/json")
	for _, route := range routes {
		lastKey := route.at[strings.LastIndex(route.at, ".")+1:]
		for _, tc := range []struct {
			name, contentType string
			body              []byte
			want              string
		}{
			{"trailing garbage", "application/json", []byte(route.body + "x"), parse},
			{"two objects", "application/json", []byte(route.body + "{}"), parse},
			{"bom", "application/json", []byte("\xef\xbb\xbf" + route.body), parse},
			{"invalid utf-8", "application/json", []byte(strings.Replace(route.body, `"gate"`, "\"gate\xff\"", 1)), official("Invalid body: encountered a unicode decode error when parsing this JSON value. Please check the value to ensure it is valid unicode.")},
			{"duplicate", "application/json", []byte(strings.Replace(route.body, `"gate"`, `"first","`+lastKey+`":"gate"`, 1)), official("Invalid body: duplicate JSON key '" + lastKey + "' at '" + route.at + "'. Duplicate JSON keys are not supported.")},
			{"string root", "application/json", []byte(`"gate"`), official("Invalid type: expected an object, but got a string instead.")},
			{"no content type", "", []byte(route.body), contentType},
			{"text/plain", "text/plain", []byte(route.body), contentType},
			{"form", "application/x-www-form-urlencoded", []byte(route.body), contentType},
			{"bodyless", "", nil, contentType},
		} {
			for _, token := range []string{owner, foreign} {
				if status, body := client.do(token, http.MethodPost, route.path, tc.contentType, tc.body); status != http.StatusBadRequest || body != tc.want {
					t.Errorf("%s %s (owner %t): %d %s", route.path, tc.name, token == owner, status, body)
				}
			}
		}
	}
	if after := databaseDigest(t, pool); !mapsEqual(before, after) {
		t.Fatal("a rejected body changed persisted state")
	}

	// B7: each valid body passes the gate unchanged; route handling decides.
	for _, route := range routes {
		status, body := client.do(owner, http.MethodPost, route.path, "application/json", []byte(route.body))
		if strings.Contains(body, "Invalid body") || strings.Contains(body, "Content-Type") || strings.Contains(body, "Invalid type") {
			t.Errorf("valid %s: %d %s", route.path, status, body)
		}
	}
	// B5 and B7: an empty update and accepted JSON media types still write.
	for _, body := range []string{``, `null`} {
		status, updated := client.do(owner, http.MethodPost, "/v1/agents/"+agent, "application/json", []byte(body))
		var fields map[string]any
		if status != http.StatusOK || json.Unmarshal([]byte(updated), &fields) != nil || fields["name"] != "body-agent" || fields["metadata"].(map[string]any)["k"] != "gate" {
			t.Fatalf("empty update %q: %d %s", body, status, updated)
		}
	}
	for _, media := range []string{"application/json; charset=utf-8", "Application/JSON", "application/merge-patch+json"} {
		status, updated := client.do(owner, http.MethodPost, "/v1/agents/"+agent, media, []byte(`{"name":"`+media+`"}`))
		if status != http.StatusOK || !strings.Contains(updated, `"name":"`+media+`"`) {
			t.Fatalf("%s update: %d %s", media, status, updated)
		}
	}
	status, created := client.do(owner, http.MethodPost, "/v1/vaults", "application/json", []byte(`null`))
	if status != http.StatusCreated || !strings.Contains(created, `"name":null`) {
		t.Fatalf("null Vault create: %d %s", status, created)
	}
	missingModel := `{"error":{"message":"Missing required parameter: 'model'.","type":"invalid_request_error","code":"invalid_request_error","param":"model"}}` + "\n"
	for _, body := range []string{``, `null`} {
		if status, response := client.do(owner, http.MethodPost, "/v1/agents", "application/json", []byte(body)); status != http.StatusBadRequest || response != missingModel {
			t.Fatalf("empty Agent create %q: %d %s", body, status, response)
		}
	}
	// The other tenant's view is unchanged.
	if status, body := client.do(foreign, http.MethodGet, "/v1/agents", "", nil); status != http.StatusOK || !strings.Contains(body, `"data":[]`) {
		t.Fatalf("foreign list: %d %s", status, body)
	}
}
