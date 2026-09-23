package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestMCPCredentialReferenceIsSchemaNotAuthorization(t *testing.T) {
	for _, saved := range []bool{false, true} {
		input := strings.TrimSuffix(publicMCP, "}") + `,"credential_id":"unresolved-reference"}`
		raw, err := resolveMCPTool(json.RawMessage(input), saved)
		var output v1.MCPTool
		if err != nil || json.Unmarshal(raw, &output) != nil || output.CredentialID == nil || *output.CredentialID != "unresolved-reference" {
			t.Fatal("saved/effective schema resolved or lost the caller credential reference", err)
		}
	}
}

func TestSessionVaultTypesAndCreationIntent(t *testing.T) {
	for _, field := range []string{"", `,"vault_ids":null`, `,"vault_ids":[]`, `,"vault_ids":["vault"]`, `,"vault_ids":[null]`, `,"vault_ids":[3]`, `,"vault_ids":{}`, `,"vault_ids":"vault"`} {
		var decoded decodedSessionRequest
		err := json.Unmarshal([]byte(`{"agent":{"model":"model"},"environment":{"type":"none"}`+field+`}`), &decoded)
		if err != nil {
			t.Fatal(err)
		}
		input, err := decoded.validated()
		invalid := strings.Contains(field, "[null]") || strings.Contains(field, "[3]") || strings.Contains(field, "{}") || strings.Contains(field, `:"vault"`)
		if (err != nil) != invalid {
			t.Fatal("Vault IDs type/default decision differed", field)
		}
		if invalid {
			continue
		}
		intent, err := sessionCreationRequest(input, nil)
		if err != nil || (len(intent) != 0) != (len(input.VaultIDs) > 0) {
			t.Fatal("attached inline intent missing or unrelated inline identity changed", err)
		}
	}
	var request decodedSessionRequest
	body := `{"agent":{"model":"model","tools":[` + publicMCP + `]},"environment":{"type":"none"},"vault_ids":["vault"]}`
	if err := json.Unmarshal([]byte(body), &request); err != nil {
		t.Fatal(err)
	}
	input, _ := request.validated()
	first, _ := sessionCreationRequest(input, []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"original"}`)}})
	input.Stream = true
	second, _ := sessionCreationRequest(input, []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"original"}`)}})
	if !reflect.DeepEqual(first, second) {
		t.Fatal("streaming changed credential-bound creation identity")
	}
	input.VaultIDs = []string{"other"}
	changed, _ := sessionCreationRequest(input, []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"original"}`)}})
	if reflect.DeepEqual(first, changed) {
		t.Fatal("changed attachment reused caller intent")
	}
}

// MV-02: a tool without an explicit credential_id projects the credential that
// creation selected; the stored configuration is unchanged.
func TestSessionProjectionShowsSelectedMCPCredential(t *testing.T) {
	vault, credential := uuid.NewString(), uuid.NewString()
	tool := func(label, credentialID string) json.RawMessage {
		value := v1.MCPTool{Type: "mcp", ServerLabel: label, Transport: v1.MCPHTTPTransport{Type: "http", ServerURL: "https://mcp.example.test/" + label},
			ConnectionOrigin: "service", RequestMetadata: map[string]json.RawMessage{}}
		if credentialID != "" {
			value.CredentialID = &credentialID
		}
		encoded, _ := json.Marshal(value)
		return encoded
	}
	binding := func(label, credentialID string) store.MCPCredentialBinding {
		b := store.MCPCredentialBinding{ServerLabel: label, ServerURL: "https://mcp.example.test/" + label}
		if credentialID != "" {
			b.VaultID, b.CredentialID, b.AuthType = vault, credentialID, "static_bearer"
		}
		return b
	}
	function := json.RawMessage(`{"type":"function","name":"lookup","description":"","parameters":{"type":"object"},"defer_loading":false}`)
	explicit := uuid.NewString()
	cfg := configuration{Agent: v1.Agent{ID: "agent", Model: "model", Tools: []json.RawMessage{tool("implicit", ""), tool("anonymous", ""), tool("explicit", strings.ToUpper(explicit)), function}},
		Environment: v1.Environment{Type: "none"}, VaultIDs: []string{strings.ToUpper(vault)},
		MCPCredentials: []store.MCPCredentialBinding{binding("implicit", credential), binding("anonymous", ""), binding("explicit", explicit)}}
	// The stored caller intent is checked against PostgreSQL by the storedNull
	// guard in TestMCPCredentialSelectionPublicPostgres.
	raw, _ := json.Marshal(cfg)
	response, err := sessionResponse(store.Session{Configuration: raw}, "")
	if err != nil || !reflect.DeepEqual(response.VaultIDs, cfg.VaultIDs) {
		t.Fatal("public attachments lost", err)
	}
	// The stored member order, here the struct order rather than alphabetical,
	// is kept by the projection.
	toolKeys := []string{"type", "server_label", "transport", "allowed_tools", "connection_origin", "credential_id", "request_metadata", "required"}
	want := []any{credential, nil, strings.ToUpper(explicit)}
	for index, expected := range want {
		var projected map[string]any
		if json.Unmarshal(response.Agent.Tools[index], &projected) != nil || !reflect.DeepEqual(projected["credential_id"], expected) {
			t.Fatalf("tool %d projected %s; want credential_id %v", index, response.Agent.Tools[index], expected)
		}
		var original map[string]any
		_ = json.Unmarshal(cfg.Agent.Tools[index], &original)
		original["credential_id"] = expected
		if !reflect.DeepEqual(projected, original) {
			t.Fatalf("tool %d changed beyond credential_id: %s", index, response.Agent.Tools[index])
		}
		if keys, _ := orderedMembers(response.Agent.Tools[index]); !reflect.DeepEqual(keys, toolKeys) {
			t.Fatalf("tool %d member order changed: %v; want %v", index, keys, toolKeys)
		}
	}
	if string(response.Agent.Tools[3]) != string(function) {
		t.Fatal("non-MCP tool changed")
	}
	public, _ := json.Marshal(response)
	if strings.Contains(string(public), "mcp_credentials") || strings.Contains(string(public), "static_bearer") || strings.Contains(string(public), vault) {
		t.Fatal("public projection exposed private binding fields", string(public))
	}

	// A binding outside the attachments or for another server is never shown.
	for _, change := range []func(*configuration){
		func(c *configuration) { c.VaultIDs = []string{uuid.NewString()} },
		func(c *configuration) { c.VaultIDs = nil },
		func(c *configuration) { c.MCPCredentials[0].ServerURL += "/other" },
		func(c *configuration) { c.MCPCredentials[0].ServerLabel = "other" },
	} {
		changed := cfg
		changed.MCPCredentials = append([]store.MCPCredentialBinding(nil), cfg.MCPCredentials...)
		change(&changed)
		raw, _ := json.Marshal(changed)
		response, err := sessionResponse(store.Session{Configuration: raw}, "")
		if err != nil || string(response.Agent.Tools[0]) != string(changed.Agent.Tools[0]) {
			t.Fatalf("unattached or unmatched binding was projected: %s, %v", response.Agent.Tools[0], err)
		}
	}
}
