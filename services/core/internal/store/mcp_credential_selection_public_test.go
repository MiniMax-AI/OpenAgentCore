package store_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

type selectionResponse struct {
	status int
	header http.Header
	body   string
}

// MCP tool origin defaults and Session credential selection (MV-01..03, rows
// M1–M8) over real HTTP and PostgreSQL, with tenants A and B.
func TestMCPCredentialSelectionPublicPostgres(t *testing.T) {
	// An isolated database keeps the no-write digest independent of other tests.
	_, pool := store.NewManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{67}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := store.NewWithCredentialCipher(pool, cipher)
	tenantA, tokenA, tokenB := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth, err := newTestAuthenticator([]testAPIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "selection-a", TokenSHA256: device.HashCredential(tokenA), TenantID: tenantA},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "selection-b", TokenSHA256: device.HashCredential(tokenB), TenantID: uuid.NewString()},
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
	send := func(token, method, path, body, key string) selectionResponse {
		t.Helper()
		request, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("OpenAI-Beta", "agents=v1")
		request.Header.Set("Content-Type", "application/json")
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var raw bytes.Buffer
		if _, err := raw.ReadFrom(response.Body); err != nil {
			t.Fatal(err)
		}
		// Per-response headers; every other header takes part in comparisons.
		response.Header.Del("Date")
		response.Header.Del("Traceparent")
		response.Header.Del("X-Request-Id")
		response.Header.Del("Openai-Processing-Ms")
		return selectionResponse{status: response.StatusCode, header: response.Header, body: raw.String()}
	}

	const url, otherURL, openURL = "https://mcp.example.test/tools", "https://mcp.example.test/other", "https://open.example.test/mcp"
	const canary = "selection-canary-token"
	credential := func(token, vault, destination string) string {
		return client.created(token, "/v1/vaults/"+vault+"/credentials", `{"name":"selection","auth":{"type":"static_bearer","mcp_server_url":"`+destination+`","token":"`+canary+`"}}`)
	}
	attachedA := client.created(tokenA, "/v1/vaults", `{"name":"attached"}`)
	secondA := client.created(tokenA, "/v1/vaults", `{"name":"second"}`)
	selectedA := credential(tokenA, attachedA, url)
	otherDestinationA := credential(tokenA, attachedA, otherURL)
	unattachedA := credential(tokenA, secondA, url)
	vaultB := client.created(tokenB, "/v1/vaults", `{"name":"b"}`)
	otherVaultB := client.created(tokenB, "/v1/vaults", `{"name":"b-other"}`)
	credentialB := credential(tokenB, vaultB, url)

	tool := func(label, destination, extra string) string {
		return `{"type":"mcp","server_label":"` + label + `","transport":{"type":"http","server_url":"` + destination + `"}` + extra + `}`
	}
	reference := func(id string) string {
		encoded, _ := json.Marshal(id)
		return `,"credential_id":` + string(encoded)
	}
	vaults := func(ids ...string) string {
		encoded, _ := json.Marshal(ids)
		return `,"vault_ids":` + string(encoded)
	}
	inline := func(tools, rest string) string {
		return `{"agent":{"model":"selection-model","tools":[` + tools + `]},"environment":{"type":"none"},"input":"Select credentials."` + rest + `}`
	}
	failure := func(kind, message string) string {
		encoded, _ := json.Marshal(message)
		return `{"error":{"message":` + string(encoded) + `,"type":"` + kind + `","code":"` + kind + `","param":null}}` + "\n"
	}
	invalid := func(message string) string { return failure("invalid_request_error", message) }
	notAttached := func(id string) string {
		return invalid("MCP credential_id " + id + " was not found in an attached vault")
	}
	missingVault := `{"error":{"message":"Resource not found.","type":"not_found_error","code":"not_found_error","param":null}}` + "\n"
	sessionTools := func(body string) []map[string]any {
		t.Helper()
		var session struct {
			Agent struct{ Tools []map[string]any }
		}
		if json.Unmarshal([]byte(body), &session) != nil {
			t.Fatal("invalid Session body", body)
		}
		return session.Agent.Tools
	}
	storedTools := func(session string) string {
		t.Helper()
		var tools string
		if err := pool.QueryRow(t.Context(), "SELECT configuration->'agent'->'tools' FROM sessions WHERE id=$1", session).Scan(&tools); err != nil {
			t.Fatal(err)
		}
		return tools
	}
	idOf := func(body string) string {
		var value struct{ ID string }
		_ = json.Unmarshal([]byte(body), &value)
		return value.ID
	}

	// M1: an omitted or null origin on HTTP transport is exactly "service".
	savedTools := func(body string) string {
		t.Helper()
		var agent struct{ Tools json.RawMessage }
		if json.Unmarshal([]byte(body), &agent) != nil {
			t.Fatal("invalid Agent body", body)
		}
		return string(agent.Tools)
	}
	explicitAgent := send(tokenA, http.MethodPost, "/v1/agents", `{"model":"m","tools":[`+tool("records", url, `,"connection_origin":"service"`)+`]}`, "")
	if explicitAgent.status != http.StatusCreated || !strings.Contains(explicitAgent.body, `"connection_origin":"service"`) {
		t.Fatal("explicit saved origin", explicitAgent.status, explicitAgent.body)
	}
	for _, origin := range []string{"", `,"connection_origin":null`} {
		created := send(tokenA, http.MethodPost, "/v1/agents", `{"model":"m","tools":[`+tool("records", url, origin)+`]}`, "")
		if created.status != http.StatusCreated || savedTools(created.body) != savedTools(explicitAgent.body) {
			t.Fatalf("saved origin %q: %d %s", origin, created.status, created.body)
		}
		updated := send(tokenA, http.MethodPost, "/v1/agents/"+idOf(created.body), `{"tools":[`+tool("records", url, origin)+`]}`, "")
		if updated.status != http.StatusOK || savedTools(updated.body) != savedTools(explicitAgent.body) {
			t.Fatalf("updated origin %q: %d %s", origin, updated.status, updated.body)
		}
	}
	explicitSession := send(tokenA, http.MethodPost, "/v1/agents/sessions", inline(tool("records", url, `,"connection_origin":"service"`), ""), "origin-explicit")
	if explicitSession.status != http.StatusCreated {
		t.Fatal("explicit Session origin", explicitSession.status, explicitSession.body)
	}
	for index, body := range []string{
		inline(tool("records", url, ""), ""),
		inline(tool("records", url, `,"connection_origin":null`), ""),
		`{"agent_id":"` + idOf(explicitAgent.body) + `","agent":{"tools":[` + tool("records", url, "") + `]},"environment":{"type":"none"},"input":"Select credentials."}`,
	} {
		created := send(tokenA, http.MethodPost, "/v1/agents/sessions", body, "")
		if created.status != http.StatusCreated || !reflect.DeepEqual(sessionTools(created.body), sessionTools(explicitSession.body)) ||
			storedTools(idOf(created.body)) != storedTools(idOf(explicitSession.body)) {
			t.Fatalf("Session origin case %d: %d %s", index, created.status, created.body)
		}
	}
	// A Session created with an explicit origin, as before this change, recovers
	// from a same-key retry that omits it: the resolved configuration is equal.
	if retry := send(tokenA, http.MethodPost, "/v1/agents/sessions", inline(tool("records", url, ""), ""), "origin-explicit"); retry.status != http.StatusCreated || retry.body != explicitSession.body {
		t.Fatal("same-key retry without origin", retry.status, retry.body)
	}
	// Recorded caller intent (attached Vaults) keeps comparing the request itself.
	attachedExplicit := send(tokenA, http.MethodPost, "/v1/agents/sessions", inline(tool("records", url, `,"connection_origin":"service"`), vaults(attachedA)), "origin-attached")
	if attachedExplicit.status != http.StatusCreated {
		t.Fatal("attached explicit origin", attachedExplicit.status, attachedExplicit.body)
	}
	if retry := send(tokenA, http.MethodPost, "/v1/agents/sessions", inline(tool("records", url, ""), vaults(attachedA)), "origin-attached"); retry.status != http.StatusConflict || !strings.Contains(retry.body, `"code":"idempotency_conflict"`) {
		t.Fatal("recorded intent retry changed", retry.status, retry.body)
	}
	referenced := client.created(tokenA, "/v1/agents", `{"model":"m","tools":[`+tool("records", url, reference(selectedA))+`]}`)

	// M3–M8: every rejection writes nothing.
	before := databaseDigest(t, pool)
	long := strings.Repeat("c", 300)
	longURL := "https://mcp.example.test/" + strings.Repeat("u", 300)
	for _, tc := range []struct {
		name, token, body string
		status            int
		want              string
	}{
		{"M3 omitted vault_ids", tokenA, inline(tool("records", url, reference(selectedA)), ""), 400, invalid("MCP credential_id requires an attached vault")},
		{"M3 null vault_ids", tokenA, inline(tool("records", url, reference(selectedA)), `,"vault_ids":null`), 400, invalid("MCP credential_id requires an attached vault")},
		{"M3 empty vault_ids", tokenA, inline(tool("records", url, reference(selectedA)), `,"vault_ids":[]`), 400, invalid("MCP credential_id requires an attached vault")},
		{"M3 saved reference", tokenA, `{"agent_id":"` + referenced + `","environment":{"type":"none"},"input":"Select credentials."}`, 400, invalid("MCP credential_id requires an attached vault")},
		{"M4 foreign tenant", tokenA, inline(tool("records", url, reference(credentialB)), vaults(attachedA)), 400, notAttached(credentialB)},
		{"M4 unattached", tokenA, inline(tool("records", url, reference(unattachedA)), vaults(attachedA)), 400, notAttached(unattachedA)},
		{"M4 unattached B", tokenB, inline(tool("records", url, reference(credentialB)), vaults(otherVaultB)), 400, notAttached(credentialB)},
		{"M4 missing", tokenA, inline(tool("records", url, reference(store.UnknownResourceID)), vaults(attachedA)), 400, notAttached(store.UnknownResourceID)},
		{"M4 malformed", tokenA, inline(tool("records", url, reference("not-a-credential")), vaults(attachedA)), 400, notAttached("not-a-credential")},
		{"M4 unbounded", tokenA, inline(tool("records", url, reference(long)), vaults(attachedA)), 400, invalid("MCP credential_id was not found in an attached vault")},
		{"M4 unprintable", tokenA, inline(tool("records", url, reference("bad\x01id")), vaults(attachedA)), 400, invalid("MCP credential_id was not found in an attached vault")},
		{"M4 saved reference", tokenA, `{"agent_id":"` + referenced + `","environment":{"type":"none"},"input":"Select credentials."` + vaults(secondA) + `}`, 400, notAttached(selectedA)},
		{"M5 destination", tokenA, inline(tool("records", url, reference(otherDestinationA)), vaults(attachedA)), 400, invalid("MCP credential_id " + otherDestinationA + " does not match server_url " + url)},
		{"M5 unbounded URL", tokenA, inline(tool("records", longURL, reference(otherDestinationA)), vaults(attachedA)), 400, invalid("MCP credential_id " + otherDestinationA + " does not match server_url")},
		{"M6 ambiguous", tokenA, inline(tool("records", url, ""), vaults(attachedA, secondA)), 409, failure("conflict_error", "multiple attached vault credentials match MCP server_url "+url+"; specify credential_id")},
		{"M6 ambiguous stream", tokenA, strings.TrimSuffix(inline(tool("records", url, ""), vaults(attachedA, secondA)), "}") + `,"stream":true}`, 409, failure("conflict_error", "multiple attached vault credentials match MCP server_url "+url+"; specify credential_id")},
		{"M7 unknown Vault", tokenA, inline(tool("records", url, ""), vaults(uuid.NewString())), 404, missingVault},
		{"M7 foreign Vault", tokenA, inline(tool("records", url, reference(selectedA)), vaults(attachedA, vaultB)), 404, missingVault},
		{"M8 input first", tokenA, `{"agent":{"model":"selection-model","tools":[` + tool("records", url, reference(credentialB)) + `]},"environment":{"type":"none"}` + vaults(attachedA) + `}`, 400, invalid("conversation-only sessions currently require initial input")},
		{"M8 stream", tokenA, strings.TrimSuffix(inline(tool("records", url, reference(credentialB)), vaults(attachedA)), "}") + `,"stream":true}`, 400, notAttached(credentialB)},
	} {
		got := send(tc.token, http.MethodPost, "/v1/agents/sessions", tc.body, "")
		if got.status != tc.status || got.body != tc.want || got.header.Get("Content-Type") != "application/json" || strings.Contains(got.body, canary) {
			t.Errorf("%s: %d %s", tc.name, got.status, got.body)
		}
	}
	// M8: the inline configuration protocol check still precedes selection.
	if got := send(tokenA, http.MethodPost, "/v1/agents/sessions", inline(tool("records", url, reference(credentialB))+`,{"type":"bogus_tool"}`, vaults(attachedA)), ""); got.status != http.StatusBadRequest || !strings.Contains(got.body, `"param":"agent.tools[1].type"`) {
		t.Error("protocol error order", got.status, got.body)
	}
	if after := databaseDigest(t, pool); !mapsEqual(before, after) {
		t.Fatal("rejected credential selection changed persisted state")
	}

	// M4: missing, foreign-tenant and unattached references are byte-identical,
	// including headers, for one credential ID.
	foreign := send(tokenA, http.MethodPost, "/v1/agents/sessions", inline(tool("records", url, reference(credentialB)), vaults(attachedA)), "")
	unattached := send(tokenB, http.MethodPost, "/v1/agents/sessions", inline(tool("records", url, reference(credentialB)), vaults(otherVaultB)), "")
	if got := send(tokenB, http.MethodDelete, "/v1/vaults/"+vaultB+"/credentials/"+credentialB, "", ""); got.status != http.StatusOK {
		t.Fatal("delete B credential", got.status, got.body)
	}
	missing := send(tokenB, http.MethodPost, "/v1/agents/sessions", inline(tool("records", url, reference(credentialB)), vaults(vaultB)), "")
	for _, got := range []selectionResponse{unattached, missing} {
		if got.status != foreign.status || got.body != foreign.body || !reflect.DeepEqual(got.header, foreign.header) {
			t.Fatalf("M4 responses differ: %d %v %s; want %d %v %s", got.status, got.header, got.body, foreign.status, foreign.header, foreign.body)
		}
	}

	// M2: the implicitly selected credential is projected in snapshots, reads,
	// lists and events; anonymous tools stay null and explicit IDs are echoed.
	implicit := inline(tool("records", url, "")+","+tool("open", openURL, ""), vaults(attachedA))
	expectTools := func(context string, tools []map[string]any, first any) {
		t.Helper()
		if len(tools) != 2 || tools[0]["credential_id"] != first || tools[1]["credential_id"] != nil || tools[0]["connection_origin"] != "service" {
			t.Fatalf("%s: unexpected projection %v", context, tools)
		}
	}
	stream := openStream(t, server, tokenA, http.MethodPost, "/v1/agents/sessions", strings.TrimSuffix(implicit, "}")+`,"stream":true}`, "selection-stream")
	defer stream.stop()
	snapshot := func(lines sseLines, event string) (string, []map[string]any) {
		t.Helper()
		for line := lines.next(t); line != "event: "+event; line = lines.next(t) {
		}
		data, ok := strings.CutPrefix(lines.next(t), "data: ")
		var value struct {
			Session struct {
				ID    string
				Agent struct{ Tools []map[string]any }
			}
		}
		if !ok || json.Unmarshal([]byte(data), &value) != nil || strings.Contains(data, canary) || strings.Contains(data, "mcp_credentials") {
			t.Fatal("invalid event", event, data)
		}
		return value.Session.ID, value.Session.Agent.Tools
	}
	streamed, tools := snapshot(stream, "agent.session.created")
	expectTools("created event", tools, selectedA)
	live := openStream(t, server, tokenA, http.MethodGet, "/v1/agents/sessions/"+streamed+"/events", "", "")
	defer live.stop()
	if line := live.next(t); line != ": connected" {
		t.Fatal(line)
	}
	var turn string
	if err := pool.QueryRow(t.Context(), "SELECT id FROM turns WHERE session_id=$1", streamed).Scan(&turn); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionTurn(t.Context(), tenantA, streamed, turn, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnCancelled}); err != nil {
		t.Fatal(err)
	}
	_, tools = snapshot(stream, "agent.session.idle")
	expectTools("idle creation event", tools, selectedA)
	_, tools = snapshot(live, "agent.session.idle")
	expectTools("idle live event", tools, selectedA)
	if got := send(tokenA, http.MethodPost, "/v1/agents/sessions/"+streamed+"/events", `{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"Again."}]}]}]}`, ""); got.status != http.StatusAccepted {
		t.Fatal("second input", got.status, got.body)
	}
	_, tools = snapshot(live, "agent.session.in_progress")
	expectTools("in_progress live event", tools, selectedA)

	created := send(tokenA, http.MethodPost, "/v1/agents/sessions", implicit, "selection-json")
	if created.status != http.StatusCreated {
		t.Fatal("implicit selection", created.status, created.body)
	}
	session := idOf(created.body)
	expectTools("created", sessionTools(created.body), selectedA)
	// The stored caller intent stays null; the private binding keeps the selection.
	var storedNull bool
	var binding string
	if err := pool.QueryRow(t.Context(), "SELECT configuration->'agent'->'tools'->0->'credential_id' = 'null'::jsonb, configuration->'mcp_credentials'->0->>'credential_id' FROM sessions WHERE id=$1", session).Scan(&storedNull, &binding); err != nil || !storedNull || binding != selectedA {
		t.Fatal("stored caller intent or private binding changed", storedNull, binding, err)
	}
	upper := strings.ToUpper(selectedA)
	explicit := send(tokenA, http.MethodPost, "/v1/agents/sessions", inline(tool("records", url, reference(upper))+","+tool("open", openURL, ""), vaults(attachedA)), "")
	if explicit.status != http.StatusCreated {
		t.Fatal("explicit selection", explicit.status, explicit.body)
	}
	expectTools("explicit", sessionTools(explicit.body), upper)
	anonymous := send(tokenA, http.MethodPost, "/v1/agents/sessions", inline(tool("records", url, "")+","+tool("open", openURL, ""), ""), "")
	if anonymous.status != http.StatusCreated {
		t.Fatal("anonymous", anonymous.status, anonymous.body)
	}
	expectTools("without attachments", sessionTools(anonymous.body), nil)

	reads := func(context string) {
		t.Helper()
		for _, id := range []string{session, streamed} {
			got := send(tokenA, http.MethodGet, "/v1/agents/sessions/"+id, "", "")
			if got.status != http.StatusOK {
				t.Fatal(context, got.status, got.body)
			}
			expectTools(context+" retrieve", sessionTools(got.body), selectedA)
		}
		list := send(tokenA, http.MethodGet, "/v1/agents/sessions?limit=100", "", "")
		var page struct {
			Data []json.RawMessage
		}
		if list.status != http.StatusOK || json.Unmarshal([]byte(list.body), &page) != nil || strings.Contains(list.body, canary) || strings.Contains(list.body, "mcp_credentials") {
			t.Fatal(context, "list", list.status)
		}
		found := 0
		for _, item := range page.Data {
			if id := idOf(string(item)); id == session || id == streamed {
				expectTools(context+" list", sessionTools(string(item)), selectedA)
				found++
			}
		}
		if found != 2 {
			t.Fatal(context, "list omitted Sessions")
		}
		if got := send(tokenB, http.MethodGet, "/v1/agents/sessions/"+session, "", ""); got.status != http.StatusNotFound || strings.Contains(got.body, selectedA) {
			t.Fatal("tenant B read tenant A's Session", got.status, got.body)
		}
	}
	reads("before deletion")
	if got := send(tokenA, http.MethodDelete, "/v1/vaults/"+attachedA+"/credentials/"+selectedA, "", ""); got.status != http.StatusOK {
		t.Fatal("delete selected credential", got.status, got.body)
	}
	reads("after deletion")
	// Same-key retries recover the original Session and projection.
	retried := send(tokenA, http.MethodPost, "/v1/agents/sessions", implicit, "selection-json")
	if retried.status != http.StatusCreated || idOf(retried.body) != session {
		t.Fatal("retry after deletion", retried.status, retried.body)
	}
	expectTools("retry", sessionTools(retried.body), selectedA)
	// A new creation cannot select the deleted credential and stays anonymous.
	fresh := send(tokenA, http.MethodPost, "/v1/agents/sessions", implicit, "")
	if fresh.status != http.StatusCreated {
		t.Fatal("fresh creation", fresh.status, fresh.body)
	}
	expectTools("fresh", sessionTools(fresh.body), nil)
}
