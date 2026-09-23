package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCaseVariantMember(t *testing.T) {
	type inner struct {
		Name string `json:"name"`
	}
	type embedded struct {
		Shared string `json:"shared"`
		Hidden string `json:"hidden"`
	}
	type outer struct {
		embedded
		Hidden  json.RawMessage   `json:"hidden"`
		Items   []inner           `json:"items"`
		Named   map[string]*inner `json:"named"`
		Skipped string            `json:"-"`
		Plain   string
		Nested  *inner `json:"nested,omitempty"`
	}
	typ := reflect.TypeFor[outer]()
	for body, want := range map[string]bool{
		`{"shared":"a","hidden":{"Name":1},"items":[{"name":"a"}],"named":{"K":{"name":"b"}},"Plain":"c","nested":{"name":"d"},"unknown":1}`: false,
		`{"Shared":"a"}`:                        true,
		`{"HIDDEN":{}}`:                         true,
		`{"items":[{"name":"a"},{"NAME":"b"}]}`: true,
		`{"named":{"k":{"Name":"b"}}}`:          true,
		`{"nested":{"nAme":"d"}}`:               true,
		`{"plain":"c"}`:                         true,
		`{"Skipped":"x"}`:                       false,
		`{"sHaReD":1,"shared":2}`:               true,
		`{"items":"not an array","named":null}`: false,
		`{"ſhared":"long s folds to s"}`:        true,
	} {
		if got := caseVariantMember([]byte(body), typ); got != want {
			t.Errorf("%s: got %t, want %t", body, got, want)
		}
	}
}

// A case variant of a member is an unknown member, rejected with the route's
// existing unknown-member error instead of replacing the field
// (req_6ba2a50c71a4410f87a1baac855e82df).
func TestCaseVariantMembersAreUnknown(t *testing.T) {
	h, s := validationHandler(t)
	session := `{"agent":{"model":"m"},"environment":{"type":"none"},"input":"hi"`
	unknownSession := `{"error":{"message":"Request must be a JSON object containing supported fields.","type":"invalid_request_error","code":"invalid_request","param":null}}` + "\n"
	for _, body := range []string{
		session + `,"Metadata":{"k":"v"}}`,
		session + `,"metadata":{"k":"v"},"Metadata":{"k":"w"}}`,
		session + `,"Input":"replaced"}`,
		session + `,"STREAM":true}`,
	} {
		if w := bodyGateRequest(h, "/v1/agents/sessions", "application/json", []byte(body)); w.Code != http.StatusBadRequest || w.Body.String() != unknownSession {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	// Nested members keep their existing unknown-member errors.
	for _, body := range []string{
		session + `,"x_agents_core":{"Model_Provider":null}}`,
		`{"agent":{"model":"m"},"environment":{"type":"none","Type":"self_hosted"},"input":"hi"}`,
		`{"agent":{"model":"m"},"environment":{"type":"none"},"input":[{"role":"assistant","Role":"user","content":[{"type":"input_text","text":"hi"}]}]}`,
	} {
		if w := bodyGateRequest(h, "/v1/agents/sessions", "application/json", []byte(body)); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	events := "/v1/agents/sessions/" + uuid.NewString() + "/events"
	unknownEvents := `{"error":{"message":"Invalid Session input event request.","type":"invalid_request_error","code":"invalid_request","param":null}}` + "\n"
	if w := bodyGateRequest(h, events, "application/json", []byte(`{"Events":[]}`)); w.Code != http.StatusBadRequest || w.Body.String() != unknownEvents {
		t.Errorf("Events: %d %s", w.Code, w.Body)
	}
	message := `{"events":[{"type":"agent.session.input.message","input":[{"role":"assistant","Role":"user","content":[{"type":"input_text","text":"hi"}]}]}]}`
	if w := bodyGateRequest(h, events, "application/json", []byte(message)); w.Code != http.StatusBadRequest {
		t.Errorf("message Role: %d %s", w.Code, w.Body)
	}
	if s.writes != 0 {
		t.Fatalf("a case variant reached storage: %d writes", s.writes)
	}
	// Map keys are free: metadata keys differing in case are distinct.
	w := bodyGateRequest(h, "/v1/vaults", "application/json", []byte(`{"metadata":{"K":"1","k":"2"}}`))
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"metadata":{"K":"1","k":"2"}`) {
		t.Fatalf("metadata keys: %d %s", w.Code, w.Body)
	}
}

func TestCredentialCaseVariantsAreUnknown(t *testing.T) {
	h, f, _ := credentialHandler(t)
	path := "/v1/vaults/" + f.credential.VaultID + "/credentials"
	refresh := `"refresh":{"client_id":"c","refresh_token":"r","token_endpoint":"https://issuer.example/token","token_endpoint_auth":{"type":"client_secret_post","client_secret":"s"}`
	auth := `{"name":"n","auth":{"type":"mcp_oauth","mcp_server_url":"https://mcp.example/tools","access_token":"a",` + refresh
	for _, body := range []string{
		auth + `,"Client_ID":"other"}}}`,
		auth + `,"token_endpoint_auth":{"type":"client_secret_post","client_secret":"s","Client_Secret":"t"}}}}`,
		`{"name":"n","auth":{"Type":"static_bearer","mcp_server_url":"https://mcp.example/tools","token":"t"}}`,
	} {
		if w := credentialRequest(h, http.MethodPost, path, body); w.Code != http.StatusBadRequest || f.calls != 0 {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := credentialRequest(h, http.MethodPost, path, auth+`}}}`); w.Code != http.StatusCreated || f.calls != 1 {
		t.Fatalf("exact names: %d %s", w.Code, w.Body)
	}
}
