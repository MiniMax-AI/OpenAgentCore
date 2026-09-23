package store_test

import (
	"bytes"
	"encoding/json"
	"io"
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
	"github.com/jackc/pgx/v5/pgxpool"
)

type pathIDClient struct {
	t      *testing.T
	server *httptest.Server
}

func (c pathIDClient) do(token, method, path, contentType string, body []byte) (int, string) {
	c.t.Helper()
	request, err := http.NewRequest(method, c.server.URL+path, bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("OpenAI-Beta", "agents=v1")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		c.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	return response.StatusCode, string(raw)
}

func (c pathIDClient) created(token, path, body string) string {
	c.t.Helper()
	status, raw := c.do(token, http.MethodPost, path, "application/json", []byte(body))
	var value struct{ ID string }
	if (status != http.StatusOK && status != http.StatusCreated) || json.Unmarshal([]byte(raw), &value) != nil || value.ID == "" {
		c.t.Fatalf("fixture %s: %d %s", path, status, raw)
	}
	return value.ID
}

// databaseDigest detects any row insertion, update or deletion in public tables.
func databaseDigest(t *testing.T, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	rows.Close()
	digest := make(map[string]string, len(tables))
	for _, table := range tables {
		var value string
		query := `SELECT count(*)::text || ':' || coalesce(md5(string_agg(t::text, ',' ORDER BY t::text)), '') FROM "` + table + `" t`
		if err := pool.QueryRow(t.Context(), query).Scan(&value); err != nil {
			t.Fatal(table, err)
		}
		digest[table] = value
	}
	return digest
}

func TestMalformedPathIDsMatchMissingPostgres(t *testing.T) {
	// An isolated database keeps the no-write digest independent of other tests.
	_, pool := store.NewManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{61}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := store.NewWithCredentialCipher(pool, cipher)
	owner, foreign := uuid.NewString(), uuid.NewString()
	ownerTenant := uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "path-owner", TokenSHA256: device.HashCredential(owner), TenantID: ownerTenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "path-foreign", TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err := api.NewHandler(s, auth, "codex", api.WithExecution(s), api.WithSubagents(s), api.WithSkills(s), api.WithSourceFiles(s), api.WithSessionArtifacts(s))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	client := pathIDClient{t: t, server: server}

	vault := client.created(owner, "/v1/vaults", `{"name":"path-owner"}`)
	credential := client.created(owner, "/v1/vaults/"+vault+"/credentials", `{"name":"path","auth":{"type":"static_bearer","mcp_server_url":"https://mcp.example/mcp","token":"path-token"}}`)
	agent := client.created(owner, "/v1/agents", `{"model":"path-model"}`)
	template := client.created(owner, "/v1/agents/environments/templates", `{"name":"path-template"}`)
	session := client.created(owner, "/v1/agents/sessions", `{"agent":{"model":"path-model"},"environment":{"type":"none"},"input":"Keep this Session."}`)
	status, raw := client.do(owner, http.MethodGet, "/v1/agents/sessions/"+session+"/turns", "", nil)
	var turns struct{ Data []struct{ ID string } }
	if status != http.StatusOK || json.Unmarshal([]byte(raw), &turns) != nil || len(turns.Data) != 1 {
		t.Fatalf("fixture Turn: %d %s", status, raw)
	}
	turn := turns.Data[0].ID
	hosted, err := s.CreateSession(t.Context(), ownerTenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "path-environment",
		Configuration: json.RawMessage(`{"agent":{"model":"path-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace","capability_directories":[]}}`)})
	if err != nil || hosted.Environment == nil {
		t.Fatal("fixture Environment", err)
	}
	environment := hosted.Environment.ID
	skill, err := s.CreateSkill(t.Context(), ownerTenant, store.SkillArchive(t, "path-skill"))
	if err != nil {
		t.Fatal(err)
	}
	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	if err := form.WriteField("purpose", "user_data"); err != nil {
		t.Fatal(err)
	}
	part, err := form.CreateFormFile("file", "path.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("path file")); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	status, raw = client.do(owner, http.MethodPost, "/v1/files", form.FormDataContentType(), upload.Bytes())
	var file struct{ ID string }
	if status != http.StatusOK || json.Unmarshal([]byte(raw), &file) != nil || file.ID == "" {
		t.Fatalf("fixture File: %d %s", status, raw)
	}

	// Each route lists its path segments. Every owned segment exists, so a
	// replaced segment is the first lookup that can fail on that route.
	type route struct {
		method, body string
		segments     []string
		query        string
	}
	missing := uuid.NewString()
	routes := []route{
		{"GET", "", []string{"/v1/vaults/", vault}, ""},
		{"DELETE", "", []string{"/v1/vaults/", vault}, ""},
		{"GET", "", []string{"/v1/vaults/", vault, "/credentials"}, ""},
		{"POST", `{"name":"path","auth":{"type":"static_bearer","mcp_server_url":"https://mcp.example/mcp","token":"t"}}`, []string{"/v1/vaults/", vault, "/credentials"}, ""},
		{"GET", "", []string{"/v1/vaults/", vault, "/credentials/", credential}, ""},
		{"POST", `{"auth":{"type":"static_bearer","token":"t"}}`, []string{"/v1/vaults/", vault, "/credentials/", credential}, ""},
		{"DELETE", "", []string{"/v1/vaults/", vault, "/credentials/", credential}, ""},
		{"GET", "", []string{"/v1/agents/", agent}, ""},
		{"POST", `{}`, []string{"/v1/agents/", agent}, ""},
		{"DELETE", "", []string{"/v1/agents/", agent}, ""},
		{"GET", "", []string{"/v1/agents/environments/templates/", template}, ""},
		{"POST", `{}`, []string{"/v1/agents/environments/templates/", template}, ""},
		{"DELETE", "", []string{"/v1/agents/environments/templates/", template}, ""},
		{"GET", "", []string{"/v1/agents/environments/", environment}, ""},
		{"GET", "", []string{"/v1/agents/environments/", environment, "/files"}, ""},
		{"POST", `{}`, []string{"/v1/agents/environments/", environment, "/files"}, ""},
		{"GET", "", []string{"/v1/agents/sessions/", session}, ""},
		{"POST", `{"metadata":{}}`, []string{"/v1/agents/sessions/", session}, ""},
		{"DELETE", "", []string{"/v1/agents/sessions/", session}, ""},
		{"POST", `{"events":[]}`, []string{"/v1/agents/sessions/", session, "/events"}, ""},
		{"GET", "", []string{"/v1/agents/sessions/", session, "/events"}, ""},
		{"GET", "", []string{"/v1/agents/sessions/", session, "/items"}, ""},
		{"GET", "", []string{"/v1/agents/sessions/", session, "/turns"}, ""},
		{"GET", "", []string{"/v1/agents/sessions/", session, "/turns/", turn}, ""},
		{"GET", "", []string{"/v1/agents/sessions/", session, "/subagents"}, ""},
		{"GET", "", []string{"/v1/agents/sessions/", session, "/subagents/", missing}, ""},
		{"GET", "", []string{"/v1/agents/sessions/", session, "/subagents/", missing, "/items"}, ""},
		{"GET", "", []string{"/v1/agents/sessions/", session, "/subagents/", missing, "/turns"}, ""},
		{"GET", "", []string{"/v1/agents/sessions/", session, "/subagents/", missing, "/turns/", missing}, ""},
		{"GET", "", []string{"/v1/agents/sessions/", session, "/subagents/", missing, "/turns/", missing, "/items"}, ""},
		{"GET", "", []string{"/v1/agents/sessions/", session, "/artifacts"}, ""},
		{"GET", "", []string{"/v1/agents/sessions/", session, "/artifacts/", missing}, ""},
		{"GET", "", []string{"/v1/agents/sessions/", session, "/artifacts/", missing, "/content"}, ""},
		{"DELETE", "", []string{"/v1/agents/sessions/", session, "/artifacts/", missing}, ""},
		{"GET", "", []string{"/v1/files/", file.ID}, ""},
		{"GET", "", []string{"/v1/files/", file.ID, "/content"}, ""},
		{"DELETE", "", []string{"/v1/files/", file.ID}, ""},
		{"GET", "", []string{"/v1/skills/", skill.ID}, ""},
		{"POST", `{"default_version":"1"}`, []string{"/v1/skills/", skill.ID}, ""},
		{"DELETE", "", []string{"/v1/skills/", skill.ID}, ""},
		{"GET", "", []string{"/v1/skills/", skill.ID, "/content"}, ""},
		{"GET", "", []string{"/v1/skills/", skill.ID, "/versions"}, ""},
		{"GET", "", []string{"/v1/skills/", skill.ID, "/versions/", "1"}, ""},
		{"DELETE", "", []string{"/v1/skills/", skill.ID, "/versions/", "1"}, ""},
		{"GET", "", []string{"/v1/skills/", skill.ID, "/versions/", "1", "/content"}, ""},
	}
	wellFormedMissing := func(owned string) string {
		switch {
		case strings.HasPrefix(owned, "file-"):
			return "file-" + uuid.NewString()
		case strings.HasPrefix(owned, "skill_"):
			return "skill_" + uuid.NewString()
		case owned == "1":
			return "999"
		}
		return uuid.NewString()
	}
	malformed := func(owned string) []string {
		values := []string{"not-a-uuid", "sess_0123456789abcdef0123456789abcdef", "00000000-0000-0000-0000-000000000000", strings.ToUpper(owned) + "0"}
		switch {
		case strings.HasPrefix(owned, "file-"), strings.HasPrefix(owned, "skill_"):
			values = append(values, strings.TrimPrefix(strings.TrimPrefix(owned, "file-"), "skill_"), owned+"x")
		case owned == "1":
			values = []string{"abc", "0", "-1", "01", "1.0", "latest"}
		}
		return values
	}
	path := func(segments []string, index int, value string) string {
		parts := append([]string{}, segments...)
		parts[index] = value
		return strings.Join(parts, "")
	}
	contentType := func(body string) string {
		if body == "" {
			return ""
		}
		return "application/json"
	}
	before := databaseDigest(t, pool)
	checked := 0
	for _, r := range routes {
		for index := 1; index < len(r.segments); index += 2 {
			owned := r.segments[index]
			wantStatus, wantBody := client.do(owner, r.method, path(r.segments, index, wellFormedMissing(owned)), contentType(r.body), []byte(r.body))
			if wantStatus != http.StatusNotFound {
				t.Fatalf("%s %s missing segment %d: %d %s", r.method, strings.Join(r.segments, ""), index, wantStatus, wantBody)
			}
			for _, value := range malformed(owned) {
				status, body := client.do(owner, r.method, path(r.segments, index, value), contentType(r.body), []byte(r.body))
				if status != wantStatus || body != wantBody {
					t.Errorf("%s %s: malformed %q = %d %s; missing = %d %s", r.method, path(r.segments, index, "{id}"), value, status, body, wantStatus, wantBody)
				}
				checked++
			}
		}
		// A foreign tenant cannot distinguish the owner's complete path from a missing one.
		wantStatus, wantBody := client.do(foreign, r.method, path(r.segments, 1, wellFormedMissing(r.segments[1])), contentType(r.body), []byte(r.body))
		status, body := client.do(foreign, r.method, strings.Join(r.segments, ""), contentType(r.body), []byte(r.body))
		malformedStatus, malformedBody := client.do(foreign, r.method, path(r.segments, 1, "not-a-uuid"), contentType(r.body), []byte(r.body))
		if wantStatus != http.StatusNotFound || status != wantStatus || body != wantBody || malformedStatus != wantStatus || malformedBody != wantBody {
			t.Errorf("%s %s: foreign %d %s, malformed %d %s; missing %d %s", r.method, strings.Join(r.segments, ""), status, body, malformedStatus, malformedBody, wantStatus, wantBody)
		}
		checked++
	}
	// Equality also holds when the request itself is invalid: body and query
	// validation run exactly as for a well-formed missing identifier.
	compare := func(r route, target, contentType string, body []byte) {
		t.Helper()
		for index := 1; index < len(r.segments); index += 2 {
			owned := r.segments[index]
			wantStatus, wantBody := client.do(target, r.method, path(r.segments, index, wellFormedMissing(owned))+r.query, contentType, body)
			for _, value := range malformed(owned) {
				status, got := client.do(target, r.method, path(r.segments, index, value)+r.query, contentType, body)
				if status != wantStatus || got != wantBody {
					t.Errorf("%s %s%s %s: malformed %q = %d %s; missing = %d %s", r.method, path(r.segments, index, "{id}"), r.query, body, value, status, got, wantStatus, wantBody)
				}
				checked++
			}
		}
	}
	for _, r := range routes {
		switch r.method {
		case http.MethodPost:
			for _, body := range []string{`{"unsupported_field":true}`, `[]`, `not json`} {
				compare(r, owner, "application/json", []byte(body))
			}
		case http.MethodDelete:
			compare(r, owner, "application/json", []byte(`{"unexpected":true}`))
		default:
			for _, query := range []string{"?limit=abc", "?order=sideways", "?limit=101&after=not-a-uuid"} {
				r.query = query
				compare(r, owner, "", nil)
			}
		}
	}
	for _, r := range []route{
		{method: "POST", body: `{"name":" ","auth":{"type":"static_bearer","mcp_server_url":"https://mcp.example/mcp","token":"t"}}`, segments: []string{"/v1/vaults/", vault, "/credentials"}},
		{method: "POST", body: `{"name":"path","auth":{"type":"static_bearer","mcp_server_url":"http://mcp.example/mcp","token":"t"}}`, segments: []string{"/v1/vaults/", vault, "/credentials"}},
		{method: "POST", body: `{"auth":{"type":"static_bearer","token":""}}`, segments: []string{"/v1/vaults/", vault, "/credentials/", credential}},
		{method: "POST", body: `{"auth":{"type":"mcp_oauth"}}`, segments: []string{"/v1/vaults/", vault, "/credentials/", credential}},
		{method: "POST", body: `{"name":"` + strings.Repeat("n", 129) + `"}`, segments: []string{"/v1/agents/", agent}},
		{method: "POST", body: `{"metadata":{"k":1}}`, segments: []string{"/v1/agents/", agent}},
		{method: "POST", body: `{"instructions":"` + strings.Repeat("x", 600*1024) + `"}`, segments: []string{"/v1/agents/", agent}},
		{method: "POST", body: `{"events":[{"type":"agent.session.input.message","input":"Admit this input."}]}`, segments: []string{"/v1/agents/sessions/", session, "/events"}},
		{method: "POST", body: `{"network":{"access":"restricted"}}`, segments: []string{"/v1/agents/environments/templates/", template}},
		{method: "POST", body: `{"packages":{"cargo":["x"]}}`, segments: []string{"/v1/agents/environments/templates/", template}},
		{method: "POST", body: `{}`, segments: []string{"/v1/agents/sessions/", session}},
		{method: "POST", body: `{"metadata":{"k":null}}`, segments: []string{"/v1/agents/sessions/", session}},
		{method: "POST", body: `{"events":null}`, segments: []string{"/v1/agents/sessions/", session, "/events"}},
		{method: "POST", body: `{"events":[{"type":"unknown"}]}`, segments: []string{"/v1/agents/sessions/", session, "/events"}},
		{method: "POST", body: `{"default_version":"abc"}`, segments: []string{"/v1/skills/", skill.ID}},
		{method: "POST", body: `{"default_version":""}`, segments: []string{"/v1/skills/", skill.ID}},
		{method: "GET", query: "?status=bogus", segments: []string{"/v1/vaults/", vault, "/credentials"}},
		{method: "GET", query: "?path=relative", segments: []string{"/v1/agents/environments/", environment, "/files"}},
	} {
		compare(r, owner, contentType(r.body), []byte(r.body))
	}
	// Skill version creation validates the multipart archive before the Skill lookup.
	versionRoute := route{method: http.MethodPost, segments: []string{"/v1/skills/", skill.ID, "/versions"}}
	for _, upload := range []struct {
		archive []byte
		fields  map[string]string
	}{
		{store.SkillArchive(t, "path-version"), nil},
		{store.SkillArchive(t, "path-version-default"), map[string]string{"default": "true"}},
		{[]byte("not a ZIP archive"), nil},
		{store.SkillArchive(t, "path-version-invalid-default"), map[string]string{"default": "maybe"}},
	} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		for name, value := range upload.fields {
			if err := form.WriteField(name, value); err != nil {
				t.Fatal(err)
			}
		}
		part, err := form.CreateFormFile("files", "proof.zip")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(upload.archive); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		compare(versionRoute, owner, form.FormDataContentType(), body.Bytes())
		missingPath := "/v1/skills/skill_" + uuid.NewString() + "/versions"
		wantStatus, wantBody := client.do(foreign, http.MethodPost, missingPath, form.FormDataContentType(), body.Bytes())
		for _, target := range []string{strings.Join(versionRoute.segments, ""), "/v1/skills/not-a-skill/versions"} {
			if status, got := client.do(foreign, http.MethodPost, target, form.FormDataContentType(), body.Bytes()); status != wantStatus || got != wantBody {
				t.Errorf("foreign Skill version %s = %d %s; missing %d %s", target, status, got, wantStatus, wantBody)
			}
		}
		if upload.fields == nil && string(upload.archive) != "not a ZIP archive" && wantStatus != http.StatusNotFound {
			t.Errorf("valid Skill version upload to a missing Skill = %d %s", wantStatus, wantBody)
		}
		checked++
	}
	if checked < 600 {
		t.Fatalf("route matrix checked only %d cases", checked)
	}

	// Storage availability checks also run before the lookup of a missing identifier.
	h, err = api.NewHandler(store.New(pool), auth, "codex")
	if err != nil {
		t.Fatal(err)
	}
	unconfigured := httptest.NewServer(h)
	defer unconfigured.Close()
	keyless := pathIDClient{t: t, server: unconfigured}
	for _, r := range []route{
		{method: "POST", body: `{"name":"path","auth":{"type":"static_bearer","mcp_server_url":"https://mcp.example/mcp","token":"t"}}`, segments: []string{"/v1/vaults/", vault, "/credentials"}},
		{method: "POST", body: `{"auth":{"type":"static_bearer","token":"t"}}`, segments: []string{"/v1/vaults/", vault, "/credentials/", credential}},
		{method: "POST", body: `{"env":{"PATH_ID":"value"}}`, segments: []string{"/v1/agents/environments/templates/", template}},
	} {
		for index := 1; index < len(r.segments); index += 2 {
			wantStatus, wantBody := keyless.do(owner, r.method, path(r.segments, index, uuid.NewString()), "application/json", []byte(r.body))
			status, body := keyless.do(owner, r.method, path(r.segments, index, "not-a-uuid"), "application/json", []byte(r.body))
			if status != wantStatus || body != wantBody {
				t.Errorf("keyless %s %s: malformed %d %s; missing %d %s", r.method, path(r.segments, index, "{id}"), status, body, wantStatus, wantBody)
			}
		}
	}

	// Malformed list cursors remain invalid requests rather than missing resources.
	for _, list := range []string{"/turns", "/items", "/subagents", "/artifacts"} {
		status, body := client.do(owner, http.MethodGet, "/v1/agents/sessions/"+session+list+"?after=not-a-uuid", "", nil)
		if status != http.StatusBadRequest || !strings.Contains(body, `"code":"invalid_request"`) {
			t.Errorf("malformed %s cursor = %d %s", list, status, body)
		}
		missingStatus, missingBody := client.do(owner, http.MethodGet, "/v1/agents/sessions/"+missing+list+"?after=not-a-uuid", "", nil)
		status, body = client.do(owner, http.MethodGet, "/v1/agents/sessions/not-a-uuid"+list+"?after=not-a-uuid", "", nil)
		if missingStatus != http.StatusNotFound || status != missingStatus || body != missingBody {
			t.Errorf("malformed Session with malformed %s cursor = %d %s; missing %d %s", list, status, body, missingStatus, missingBody)
		}
	}
	// Top-level cursors keep their existing family behavior; Templates already
	// resolved malformed cursors as missing before this change.
	for list, want := range map[string]int{"/v1/agents/sessions": 400, "/v1/agents": 400, "/v1/vaults": 400, "/v1/vaults/" + vault + "/credentials": 400, "/v1/agents/environments/templates": 404} {
		if status, body := client.do(owner, http.MethodGet, list+"?after=not-a-uuid", "", nil); status != want {
			t.Errorf("malformed %s cursor = %d %s; want %d", list, status, body, want)
		}
	}
	if after := databaseDigest(t, pool); !mapsEqual(before, after) {
		t.Fatal("malformed, missing or foreign path identifiers changed persisted state")
	}
	// Owned resources remain readable after the rejected mutations.
	for _, owned := range []string{"/v1/vaults/" + vault, "/v1/vaults/" + vault + "/credentials/" + credential, "/v1/agents/" + agent, "/v1/agents/environments/templates/" + template, "/v1/agents/sessions/" + session, "/v1/agents/sessions/" + session + "/turns/" + turn, "/v1/files/" + file.ID, "/v1/skills/" + skill.ID + "/versions/1"} {
		if status, body := client.do(owner, http.MethodGet, owned, "", nil); status != http.StatusOK {
			t.Errorf("owned %s = %d %s", owned, status, body)
		}
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
