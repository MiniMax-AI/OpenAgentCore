package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/google/uuid"
)

// TV-05: saved Agents keep every pinned web_search mode with the official
// projection (W1/W2). Codex declares cached and live search, so saved and inline
// search reach the Session unchanged (W4/W6); a tools replacement replaces it
// (W5) and tenant isolation holds (W8).
func TestSavedWebSearchPostgres(t *testing.T) {
	// An isolated database keeps the no-write digest independent of other tests.
	s, _ := newManagedTestStore(t)
	owner, foreign, ownerTenant := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "search-owner", TokenSHA256: runtimedevice.HashCredential(owner), TenantID: ownerTenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "search-foreign", TokenSHA256: runtimedevice.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	h, err := publicHandler(t, s, auth, "codex")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	client := pathIDClient{t: t, server: server}

	// tools returns the exact tool bytes of an Agent response and checks them
	// against the official projection.
	tools := func(name string, status int, body string, want int, official string) string {
		t.Helper()
		var agent struct {
			ID    string
			Tools []json.RawMessage
		}
		if status != want || json.Unmarshal([]byte(body), &agent) != nil || len(agent.Tools) != 1 {
			t.Fatalf("%s: %d %s", name, status, body)
		}
		var got, expected any
		if json.Unmarshal(agent.Tools[0], &got) != nil || json.Unmarshal([]byte(official), &expected) != nil || !reflect.DeepEqual(got, expected) {
			t.Fatalf("%s: tool %s, official %s", name, agent.Tools[0], official)
		}
		return string(agent.Tools[0])
	}
	// readBack checks that retrieve and list return the same Agent bytes.
	readBack := func(name, id, body string) {
		t.Helper()
		if status, retrieved := client.do(owner, http.MethodGet, "/v1/agents/"+id, "", nil); status != http.StatusOK || retrieved != body {
			t.Fatalf("%s retrieve: %d %s", name, status, retrieved)
		}
		status, raw := client.do(owner, http.MethodGet, "/v1/agents?limit=100", "", nil)
		var page struct{ Data []json.RawMessage }
		if status != http.StatusOK || json.Unmarshal([]byte(raw), &page) != nil {
			t.Fatalf("%s list: %d %s", name, status, raw)
		}
		for _, item := range page.Data {
			if strings.Contains(string(item), `"id":"`+id+`"`) {
				if string(item)+"\n" != body {
					t.Fatalf("%s list item %s, want %s", name, item, body)
				}
				return
			}
		}
		t.Fatalf("%s: Agent missing from list", name)
	}
	const defaults = `"context_size":"medium","allowed_domains":null,"location":null}`
	live := `{"type":"web_search","mode":"live",` + defaults
	cached := `{"type":"web_search","mode":"cached",` + defaults

	// W1: Agent create with every pinned mode (official create 201).
	agentIDs := map[string]string{}
	for _, tc := range []struct{ name, tool, official string }{
		{"type-only", `{"type":"web_search"}`, live},
		{"mode-null", `{"type":"web_search","mode":null}`, live},
		{"mode-live", `{"type":"web_search","mode":"live"}`, live},
		{"mode-cached", `{"type":"web_search","mode":"cached"}`, cached},
		{"mode-cached-full", `{"type":"web_search","mode":"cached","context_size":"high","allowed_domains":["example.com"],"location":{"city":"Paris","country":"FR","region":null,"timezone":null}}`,
			`{"type":"web_search","mode":"cached","context_size":"high","allowed_domains":["example.com"],"location":{"country":"FR","region":null,"city":"Paris","timezone":null}}`},
		// A supplied location projects all four keys, with null for omitted ones
		// (req_db41d2f6261b4abfb69465eafe719ab5, req_165d53b88445490b9146d8272c54134d).
		{"location-partial-omitted", `{"type":"web_search","mode":"cached","location":{"city":"Paris","country":"FR"}}`,
			`{"type":"web_search","mode":"cached","context_size":"medium","allowed_domains":null,"location":{"country":"FR","region":null,"city":"Paris","timezone":null}}`},
		{"location-empty", `{"type":"web_search","mode":"cached","location":{}}`,
			`{"type":"web_search","mode":"cached","context_size":"medium","allowed_domains":null,"location":{"country":null,"region":null,"city":null,"timezone":null}}`},
		{"mode-disabled", `{"type":"web_search","mode":"disabled"}`, `{"type":"web_search","mode":"disabled",` + defaults},
	} {
		status, body := client.do(owner, http.MethodPost, "/v1/agents", "application/json", []byte(`{"model":"search-model","tools":[`+tc.tool+`]}`))
		tools(tc.name, status, body, http.StatusCreated, tc.official)
		var created struct{ ID string }
		_ = json.Unmarshal([]byte(body), &created)
		readBack(tc.name, created.ID, body)
		agentIDs[tc.name] = created.ID
	}

	// W2: update replaces tools with each observed form (official update 200).
	updated := client.created(owner, "/v1/agents", `{"model":"search-model","tools":[{"type":"web_search"}]}`)
	for _, tc := range []struct{ name, tool, official string }{
		{"update-disabled", `{"type":"web_search","mode":"disabled"}`, `{"type":"web_search","mode":"disabled",` + defaults},
		{"update-omitted-low", `{"type":"web_search","context_size":"low"}`, `{"type":"web_search","mode":"live","context_size":"low","allowed_domains":null,"location":null}`},
		{"update-live-domains-empty", `{"type":"web_search","mode":"live","allowed_domains":[]}`, `{"type":"web_search","mode":"live","context_size":"medium","allowed_domains":[],"location":null}`},
		{"update-cached-location", `{"type":"web_search","mode":"cached","location":{"timezone":"Europe/Paris"}}`,
			`{"type":"web_search","mode":"cached","context_size":"medium","allowed_domains":null,"location":{"city":null,"country":null,"region":null,"timezone":"Europe/Paris"}}`},
	} {
		status, body := client.do(owner, http.MethodPost, "/v1/agents/"+updated, "application/json", []byte(`{"tools":[`+tc.tool+`]}`))
		tools(tc.name, status, body, http.StatusOK, tc.official)
		readBack(tc.name, updated, body)
	}

	// A saved disabled search reads unchanged and is admitted with the same frozen
	// Session tool (W7).
	_, agentService := fixtureAgents(t, s)
	savedDisabled, err := agentService.Create(t.Context(), agents.CreateCommand{TenantID: ownerTenant, Metadata: map[string]string{}, Configuration: json.RawMessage(
		`{"model":"search-model","name":null,"instructions":null,"multi_agent":{"enabled":false,"max_concurrent_subagents":null},"reasoning":{},"service_tier":"auto","text":{"format":{"type":"text"},"verbosity":"medium"},"tools":[{"type":"web_search","mode":"disabled","context_size":"medium","allowed_domains":[],"location":null}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	status, body := client.do(owner, http.MethodGet, "/v1/agents/"+savedDisabled.ID, "", nil)
	disabledTool := tools("disabled", status, body, http.StatusOK, `{"type":"web_search","mode":"disabled","context_size":"medium","allowed_domains":[],"location":null}`)
	readBack("disabled", savedDisabled.ID, body)
	// sessionTool returns the first frozen Session tool in canonical form; Session
	// snapshots keep their own key order.
	sessionTool := func(name, session string) string {
		t.Helper()
		status, raw := client.do(owner, http.MethodGet, "/v1/agents/sessions/"+session, "", nil)
		var value struct {
			Agent struct{ Tools []json.RawMessage }
		}
		if status != http.StatusOK || json.Unmarshal([]byte(raw), &value) != nil {
			t.Fatalf("%s Session: %d %s", name, status, raw)
		}
		if len(value.Agent.Tools) == 0 {
			return ""
		}
		return canonicalTool(t, value.Agent.Tools[0])
	}
	if got := sessionTool("disabled", client.created(owner, "/v1/agents/sessions", `{"agent_id":"`+savedDisabled.ID+`","environment":{"type":"none"},"input":"hi"}`)); got != canonicalTool(t, json.RawMessage(disabledTool)) {
		t.Fatalf("disabled Session tool %s, want %s", got, disabledTool)
	}

	// A same-key retry of a Session that recorded its creation request recovers it
	// after the Agent enables search.
	retry := func(key string) (int, string) {
		request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/agents/sessions", strings.NewReader(`{"agent_id":"`+agentIDs["mode-disabled"]+`","environment":{"type":"none"},"input":"hi"}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+owner)
		request.Header.Set("OpenAI-Beta", "agents=v1")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var session struct{ ID string }
		_ = json.NewDecoder(response.Body).Decode(&session)
		return response.StatusCode, session.ID
	}
	status, original := retry("before-search")
	if status != http.StatusCreated || original == "" {
		t.Fatalf("retry fixture: %d", status)
	}
	client.created(owner, "/v1/agents/"+agentIDs["mode-disabled"], `{"tools":[{"type":"web_search","mode":"live"}]}`)
	enabledTools := map[string]struct{ id, tool string }{"mode-live": {agentIDs["mode-live"], live}, "mode-cached": {agentIDs["mode-cached"], cached}, "type-only": {agentIDs["type-only"], live}, "updated-to-live": {agentIDs["mode-disabled"], live}}

	// W4: each saved search reaches a new Session unchanged.
	for name, saved := range enabledTools {
		session := client.created(owner, "/v1/agents/sessions", `{"agent_id":"`+saved.id+`","environment":{"type":"none"},"input":"hi"}`)
		if got := sessionTool(name, session); got != canonicalTool(t, json.RawMessage(saved.tool)) {
			t.Errorf("%s Session tool %s, want %s", name, got, saved.tool)
		}
	}
	// W6: inline enabled or omitted-mode search is admitted the same way.
	for tool, want := range map[string]string{`{"type":"web_search"}`: live, `{"type":"web_search","mode":null}`: live, `{"type":"web_search","mode":"cached"}`: cached} {
		for _, request := range []string{
			`{"agent":{"model":"m","tools":[` + tool + `]},"environment":{"type":"none"},"input":"hi"}`,
			`{"agent_id":"` + savedDisabled.ID + `","agent":{"tools":[` + tool + `]},"environment":{"type":"none"},"input":"hi"}`,
		} {
			if got := sessionTool(request, client.created(owner, "/v1/agents/sessions", request)); got != canonicalTool(t, json.RawMessage(want)) {
				t.Errorf("inline %s: Session tool %s, want %s", request, got, want)
			}
		}
	}

	before := databaseDigest(t, s.pool)
	// C4: the input requirement still applies to a saved search Agent.
	const input = `{"error":{"message":"conversation-only sessions currently require initial input","type":"invalid_request_error","code":"invalid_request_error","param":null}}` + "\n"
	for name, saved := range enabledTools {
		if status, body := client.do(owner, http.MethodPost, "/v1/agents/sessions", "application/json", []byte(`{"agent_id":"`+saved.id+`","environment":{"type":"none"}}`)); status != http.StatusBadRequest || body != input {
			t.Errorf("%s without input: %d %s", name, status, body)
		}
	}
	// W8: another tenant sees the same 404 as a missing Agent and writes nothing.
	missing := uuid.NewString()
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/agents/%s", ""},
		{http.MethodPost, "/v1/agents/%s", `{"tools":[{"type":"web_search","mode":"disabled"}]}`},
		{http.MethodPost, "/v1/agents/sessions", `{"agent_id":"%s","environment":{"type":"none"},"input":"hi"}`},
		{http.MethodPost, "/v1/agents/sessions", `{"agent_id":"%s","agent":{"tools":[]},"environment":{"type":"none"},"input":"hi"}`},
	} {
		send := func(token, id string) (int, string) {
			return client.do(token, request.method, strings.ReplaceAll(request.path, "%s", id), "application/json", []byte(strings.ReplaceAll(request.body, "%s", id)))
		}
		foreignStatus, foreignBody := send(foreign, agentIDs["mode-live"])
		missingStatus, missingBody := send(owner, missing)
		if foreignStatus != http.StatusNotFound || foreignStatus != missingStatus || foreignBody != missingBody {
			t.Errorf("%s %s: foreign %d %s, missing %d %s", request.method, request.path, foreignStatus, foreignBody, missingStatus, missingBody)
		}
	}
	if status, raw := client.do(foreign, http.MethodGet, "/v1/agents?limit=100", "", nil); status != http.StatusOK || strings.Contains(raw, "search-model") {
		t.Errorf("foreign list: %d %s", status, raw)
	}
	if after := databaseDigest(t, s.pool); !mapsEqual(before, after) {
		t.Fatal("rejected Session creation changed persisted state")
	}

	// Same-key retries keep their rule: the recorded Session is returned unchanged.
	if status, recovered := retry("before-search"); status != http.StatusCreated || recovered != original {
		t.Fatalf("same-key retry: %d %s, want %s", status, recovered, original)
	}
	disabled := canonicalTool(t, json.RawMessage(`{"type":"web_search","mode":"disabled",`+defaults))
	if got := sessionTool("retry", original); got != disabled {
		t.Fatalf("recovered Session tool %s", got)
	}
	if status, session := retry("after-search"); status != http.StatusCreated || session == original {
		t.Fatalf("new key after enabling search: %d %s", status, session)
	}

	// W5: a per-Session tools replacement replaces the saved search.
	for _, replacement := range []string{`[]`, `[{"type":"web_search","mode":"disabled"}]`} {
		session := client.created(owner, "/v1/agents/sessions", `{"agent_id":"`+agentIDs["mode-live"]+`","agent":{"tools":`+replacement+`},"environment":{"type":"none"},"input":"hi"}`)
		if got := sessionTool("replacement", session); got != "" && got != disabled {
			t.Fatalf("replacement %s: Session tool %s", replacement, got)
		}
	}
}

// canonicalTool re-encodes a tool with sorted keys for comparison across resources.
func canonicalTool(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
