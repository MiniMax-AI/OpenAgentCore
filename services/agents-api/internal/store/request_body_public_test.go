package store_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
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
	auth, err := newTestAuthenticator([]testAPIKey{
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
	// Member names match exactly: a case variant is an unknown member, never an
	// alias whose value replaces the field (req_6ba2a50c71a4410f87a1baac855e82df).
	message := `[{"role":"assistant","Role":"user","content":[{"type":"input_text","text":"case"}]}]`
	for _, request := range []struct{ path, body string }{
		{"/v1/agents/sessions", `{"agent":{"model":"m"},"environment":{"type":"none"},"input":"case","Metadata":{"k":"v"}}`},
		{"/v1/agents/sessions", `{"agent":{"model":"m"},"environment":{"type":"none"},"Input":"case"}`},
		{"/v1/agents/sessions", `{"agent":{"model":"m"},"environment":{"type":"none"},"input":` + message + `}`},
		{"/v1/agents/sessions/" + session + "/events", `{"Events":[{"type":"agent.session.input.cancel"}]}`},
		{"/v1/agents/sessions/" + session + "/events", `{"events":[{"type":"agent.session.input.message","input":` + message + `}]}`},
		{"/v1/vaults/" + vault + "/credentials", `{"name":"case","auth":{"type":"mcp_oauth","mcp_server_url":"https://mcp.example/mcp","access_token":"a","refresh":{"client_id":"c","Client_ID":"d","refresh_token":"r","token_endpoint":"https://issuer.example/token","token_endpoint_auth":{"type":"none"}}}}`},
	} {
		for _, token := range []string{owner, foreign} {
			if status, body := client.do(token, http.MethodPost, request.path, "application/json", []byte(request.body)); status != http.StatusBadRequest && status != http.StatusNotFound {
				t.Errorf("%s %s: %d %s", request.path, request.body, status, body)
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

// DELETE routes, the multipart Files and Skills uploads, Skills update and the
// Core extension routes keep their own body handling, without the Content-Type
// rule or the official body messages.
func TestRequestBodyGateExcludedRoutesPostgres(t *testing.T) {
	_, pool := store.NewTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{65}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := store.NewWithCredentialCipher(pool, cipher)
	token, tenant := uuid.NewString(), uuid.NewString()
	auth, err := newTestAuthenticator([]testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "excluded-owner", TokenSHA256: device.HashCredential(token), TenantID: tenant}})
	if err != nil {
		t.Fatal(err)
	}
	h, err := api.NewHandler(s, auth, "codex", api.WithExecution(s), api.WithSkills(s), api.WithSourceFiles(s))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	client := pathIDClient{t: t, server: server}
	gated := func(body string) bool {
		return strings.Contains(body, "expected request with Content-Type") || strings.Contains(body, "Invalid body") || strings.Contains(body, "Invalid type")
	}
	upload := func(field, name string, content []byte, extra ...string) (string, []byte) {
		var buffer bytes.Buffer
		form := multipart.NewWriter(&buffer)
		for i := 0; i+1 < len(extra); i += 2 {
			if err := form.WriteField(extra[i], extra[i+1]); err != nil {
				t.Fatal(err)
			}
		}
		part, err := form.CreateFormFile(field, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		return form.FormDataContentType(), buffer.Bytes()
	}
	contentType, body := upload("file", "excluded.txt", []byte("excluded"), "purpose", "user_data")
	if status, response := client.do(token, http.MethodPost, "/v1/files", contentType, body); status != http.StatusOK || gated(response) {
		t.Fatalf("Files upload: %d %s", status, response)
	}
	contentType, body = upload("files", "proof.zip", store.SkillArchive(t, "excluded-skill"))
	status, response := client.do(token, http.MethodPost, "/v1/skills", contentType, body)
	var skill struct{ ID string }
	if status != http.StatusOK || json.Unmarshal([]byte(response), &skill) != nil || skill.ID == "" {
		t.Fatalf("Skills upload: %d %s", status, response)
	}
	if status, response := client.do(token, http.MethodPost, "/v1/skills/"+skill.ID, "", []byte(`{"default_version":"1"}`)); status != http.StatusOK || gated(response) {
		t.Fatalf("Skills update without Content-Type: %d %s", status, response)
	}
	if status, response := client.do(token, http.MethodPost, "/v1/skills/"+skill.ID, "text/plain", []byte(`{"default_version":`)); status != http.StatusBadRequest || gated(response) {
		t.Fatalf("malformed Skills update: %d %s", status, response)
	}
	// DELETE keeps its empty-body rule and needs no Content-Type.
	agent := client.created(token, "/v1/agents", `{"model":"excluded-model"}`)
	vault := client.created(token, "/v1/vaults", `{"name":"excluded"}`)
	if status, response := client.do(token, http.MethodDelete, "/v1/vaults/"+vault, "text/plain", []byte(`{"name":`)); status != http.StatusBadRequest || gated(response) {
		t.Fatalf("Vault delete with a body: %d %s", status, response)
	}
	for _, path := range []string{"/v1/agents/" + agent, "/v1/vaults/" + vault} {
		if status, response := client.do(token, http.MethodDelete, path, "", nil); status != http.StatusOK || gated(response) {
			t.Fatalf("DELETE %s: %d %s", path, status, response)
		}
	}
}
