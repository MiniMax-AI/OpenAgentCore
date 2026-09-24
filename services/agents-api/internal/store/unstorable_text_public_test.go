package store_test

import (
	"archive/zip"
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

// PostgreSQL text and jsonb cannot store U+0000. Every persisted client string
// containing it must be rejected as a client error before anything is written.
func TestUnstorableTextRejectsWithoutWritesPostgres(t *testing.T) {
	// An isolated database keeps the no-write digest independent of other tests.
	_, pool := store.NewManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{62}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := store.NewWithCredentialCipher(pool, cipher)
	token := uuid.NewString()
	auth, err := newTestAuthenticator([]testAPIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "nul-owner", TokenSHA256: device.HashCredential(token), TenantID: uuid.NewString()}})
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
	vault := client.created(token, "/v1/vaults", `{"name":"nul-vault"}`)
	agent := client.created(token, "/v1/agents", `{"model":"nul-model"}`)
	template := client.created(token, "/v1/agents/environments/templates", `{"name":"nul-template"}`)
	session := client.created(token, "/v1/agents/sessions", `{"agent":{"model":"nul-model"},"environment":{"type":"none"},"input":"Keep this Session."}`)
	before := databaseDigest(t, pool)
	const nul = `a\u0000b`
	keyParam, valueParam := "metadata.a\x00b", "metadata.k"
	for _, tc := range []struct {
		name, method, path, body string
		param                    *string
	}{
		{"vault metadata value", "POST", "/v1/vaults", `{"metadata":{"k":"` + nul + `"}}`, &valueParam},
		{"vault metadata key", "POST", "/v1/vaults", `{"metadata":{"` + nul + `":"v"}}`, &keyParam},
		{"agent create metadata", "POST", "/v1/agents", `{"model":"m","metadata":{"k":"` + nul + `"}}`, &valueParam},
		{"agent update metadata", "POST", "/v1/agents/" + agent, `{"metadata":{"` + nul + `":"v"}}`, &keyParam},
		{"session create metadata", "POST", "/v1/agents/sessions", `{"agent":{"model":"m"},"environment":{"type":"none"},"input":"hi","metadata":{"k":"` + nul + `"}}`, &valueParam},
		{"session update metadata", "POST", "/v1/agents/sessions/" + session, `{"metadata":{"k":"` + nul + `"}}`, &valueParam},
		{"vault name", "POST", "/v1/vaults", `{"name":"` + nul + `"}`, nil},
		{"credential name", "POST", "/v1/vaults/" + vault + "/credentials", `{"name":"` + nul + `","auth":{"type":"static_bearer","mcp_server_url":"https://mcp.example/mcp","token":"t"}}`, nil},
		{"agent name", "POST", "/v1/agents", `{"model":"m","name":"` + nul + `"}`, nil},
		{"agent model", "POST", "/v1/agents", `{"model":"` + nul + `"}`, nil},
		{"agent instructions", "POST", "/v1/agents", `{"model":"m","instructions":"` + nul + `"}`, nil},
		{"agent tool", "POST", "/v1/agents", `{"model":"m","tools":[{"type":"function","name":"f","description":"` + nul + `","parameters":{"type":"object"}}]}`, nil},
		{"agent update name", "POST", "/v1/agents/" + agent, `{"name":"` + nul + `"}`, nil},
		{"template create name", "POST", "/v1/agents/environments/templates", `{"name":"` + nul + `"}`, nil},
		{"template update name", "POST", "/v1/agents/environments/templates/" + template, `{"name":"` + nul + `"}`, nil},
		{"session input", "POST", "/v1/agents/sessions", `{"agent":{"model":"m"},"environment":{"type":"none"},"input":"` + nul + `"}`, nil},
		{"session instructions", "POST", "/v1/agents/sessions", `{"agent":{"model":"m","instructions":"` + nul + `"},"environment":{"type":"none"},"input":"hi"}`, nil},
		{"session event input", "POST", "/v1/agents/sessions/" + session + "/events", `{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"` + nul + `"}]}]}]}`, nil},
	} {
		status, body := client.do(token, tc.method, tc.path, "application/json", []byte(tc.body))
		var response struct {
			Error struct {
				Type, Code string
				Param      *string
			}
		}
		if status != http.StatusBadRequest || json.Unmarshal([]byte(body), &response) != nil || response.Error.Type != "invalid_request_error" || response.Error.Code != "invalid_request_error" ||
			(tc.param == nil) != (response.Error.Param == nil) || tc.param != nil && *response.Error.Param != *tc.param {
			t.Errorf("%s: %d %s", tc.name, status, body)
		}
	}

	// Skill metadata parsed from an uploaded archive uses the same mapping.
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.Create("proof/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("---\nname: proof\ndescription: \"a\\0b\"\n---\nProof.")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	part, err := form.CreateFormFile("files", "proof.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(archive.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	if status, body := client.do(token, http.MethodPost, "/v1/skills", form.FormDataContentType(), upload.Bytes()); status != http.StatusBadRequest || !strings.Contains(body, `"code":"invalid_request_error"`) {
		t.Errorf("skill description: %d %s", status, body)
	}
	// Invalid UTF-8 in a query filter reaches the same mapping, so its message is generic.
	status, body := client.do(token, http.MethodGet, "/v1/agents/sessions?agent_id=%ff", "", nil)
	if status != http.StatusBadRequest || body != `{"error":{"message":"Request text contains characters this service cannot store or compare, such as U+0000 or invalid UTF-8.","type":"invalid_request_error","code":"invalid_request_error","param":null}}`+"\n" {
		t.Errorf("invalid UTF-8 filter: %d %s", status, body)
	}
	if after := databaseDigest(t, pool); !mapsEqual(before, after) {
		t.Error("rejected U+0000 strings changed persisted state")
	}
}
