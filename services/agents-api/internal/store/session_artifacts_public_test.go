package store_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

// hostedArtifactSession creates an openai_hosted Session without Turns.
func hostedArtifactSession(t *testing.T, s *store.Store, tenant, key string) (session, environment string) {
	t.Helper()
	created, err := s.CreateSession(t.Context(), tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: key,
		Configuration: json.RawMessage(`{"agent":{"model":"artifact-model"},"environment":{"type":"openai_hosted","workspace_directory":"/workspace","capability_directories":[]}}`)})
	if err != nil || created.Environment == nil {
		t.Fatal("fixture Session", err)
	}
	return created.ID, created.Environment.ID
}

// completeArtifactTurn runs one Turn whose complete outputs tree is captured
// and settled as completed, and returns the Turn ID.
func completeArtifactTurn(t *testing.T, s *store.Store, tenant, session, environment, key string, outputs map[string]string) string {
	t.Helper()
	receipt, err := s.SubmitMessage(t.Context(), tenant, session, key, json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"publish outputs"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	transition := func(from, to string) {
		t.Helper()
		if _, err := s.TransitionTurn(t.Context(), tenant, session, receipt.TurnID, store.TurnTransition{ExpectedStatus: from, Status: to}); err != nil {
			t.Fatal(err)
		}
	}
	transition(store.TurnQueued, store.TurnInProgress)
	var archive bytes.Buffer
	w := tar.NewWriter(&archive)
	for name, body := range outputs {
		if err := w.WriteHeader(&tar.Header{Name: "outputs/" + name, Mode: 0600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.StageTurnArtifacts(t.Context(), tenant, session, receipt.TurnID, environment, &archive); err != nil {
		t.Fatal(err)
	}
	transition(store.TurnInProgress, store.TurnCompleted)
	return receipt.TurnID
}

// artifactHTTPServer serves Artifact routes for an owner and a foreign tenant.
func artifactHTTPServer(t *testing.T, s *store.Store) (server *httptest.Server, owner, ownerTenant, foreign, foreignTenant string) {
	t.Helper()
	owner, foreign = uuid.NewString(), uuid.NewString()
	ownerTenant, foreignTenant = uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "artifact-owner", TokenSHA256: device.HashCredential(owner), TenantID: ownerTenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "artifact-foreign", TokenSHA256: device.HashCredential(foreign), TenantID: foreignTenant},
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err := api.NewHandler(s, auth, "codex", api.WithSessionArtifacts(s))
	if err != nil {
		t.Fatal(err)
	}
	server = httptest.NewServer(h)
	t.Cleanup(server.Close)
	return server, owner, ownerTenant, foreign, foreignTenant
}

// The Artifact list uses the common list envelope (HE-53), and a malformed
// environment_id filter matches nothing like another Environment's ID (HE-56),
// without weakening tenant or Session scoping.
func TestSessionArtifactListEnvelopeAndEnvironmentFilterPostgres(t *testing.T) {
	s, _ := store.NewTestStore(t)
	server, owner, ownerTenant, foreign, foreignTenant := artifactHTTPServer(t, s)
	client := pathIDClient{t: t, server: server}

	session, environment := hostedArtifactSession(t, s, ownerTenant, "artifact-list")
	completeArtifactTurn(t, s, ownerTenant, session, environment, "artifact-list-turn", map[string]string{"a.txt": "alpha", "b.txt": "bravo"})
	idle, otherEnvironment := hostedArtifactSession(t, s, ownerTenant, "artifact-idle")
	foreignSession, foreignEnvironment := hostedArtifactSession(t, s, foreignTenant, "artifact-foreign")
	completeArtifactTurn(t, s, foreignTenant, foreignSession, foreignEnvironment, "artifact-foreign-turn", map[string]string{"a.txt": "alpha"})

	type envelope struct {
		Object  *string           `json:"object"`
		FirstID *string           `json:"first_id"`
		LastID  *string           `json:"last_id"`
		Data    []json.RawMessage `json:"data"`
		HasMore *bool             `json:"has_more"`
	}
	list := func(token, session string, query url.Values) (int, string, envelope) {
		t.Helper()
		status, raw := client.do(token, http.MethodGet, "/v1/agents/sessions/"+session+"/artifacts?"+query.Encode(), "", nil)
		var page envelope
		if status == http.StatusOK {
			var keys map[string]json.RawMessage
			if json.Unmarshal([]byte(raw), &keys) != nil || json.Unmarshal([]byte(raw), &page) != nil {
				t.Fatalf("list body: %s", raw)
			}
			if len(keys) != 5 || keys["first_id"] == nil || keys["last_id"] == nil || page.Object == nil || *page.Object != "list" || page.Data == nil || page.HasMore == nil {
				t.Fatalf("list envelope fields: %s", raw)
			}
		}
		return status, raw, page
	}
	ids := func(page envelope) []string {
		t.Helper()
		out := make([]string, 0, len(page.Data))
		for _, item := range page.Data {
			var value struct{ ID string }
			if json.Unmarshal(item, &value) != nil || value.ID == "" {
				t.Fatalf("artifact item: %s", item)
			}
			out = append(out, value.ID)
		}
		return out
	}
	const empty = `{"object":"list","first_id":null,"last_id":null,"data":[],"has_more":false}` + "\n"

	// First and last IDs bound every non-empty page; paging is unchanged.
	status, raw, all := list(owner, session, url.Values{"order": {"asc"}})
	published := ids(all)
	if status != http.StatusOK || len(published) != 2 || *all.FirstID != published[0] || *all.LastID != published[1] || *all.HasMore {
		t.Fatalf("full page: %d %s", status, raw)
	}
	status, raw, first := list(owner, session, url.Values{"order": {"asc"}, "limit": {"1"}})
	if status != http.StatusOK || !reflect.DeepEqual(ids(first), published[:1]) || *first.FirstID != published[0] || *first.LastID != published[0] || !*first.HasMore {
		t.Fatalf("first page: %d %s", status, raw)
	}
	status, raw, second := list(owner, session, url.Values{"order": {"asc"}, "limit": {"1"}, "after": {published[0]}})
	if status != http.StatusOK || !reflect.DeepEqual(ids(second), published[1:]) || *second.FirstID != published[1] || *second.LastID != published[1] || *second.HasMore {
		t.Fatalf("second page: %d %s", status, raw)
	}
	status, raw, _ = list(owner, session, url.Values{"order": {"asc"}, "limit": {"1"}, "after": {published[1]}})
	if status != http.StatusOK || raw != empty {
		t.Fatalf("page after the last Artifact: %d %s", status, raw)
	}
	if status, raw, filtered := list(owner, session, url.Values{"order": {"asc"}, "environment_id": {environment}}); status != http.StatusOK || !reflect.DeepEqual(ids(filtered), published) {
		t.Fatalf("own Environment filter: %d %s", status, raw)
	}
	if status, raw, _ := list(owner, idle, nil); status != http.StatusOK || raw != empty {
		t.Fatalf("Session without Artifacts: %d %s", status, raw)
	}

	// Another existing, foreign, unknown or malformed Environment ID: an empty page.
	for _, filter := range []string{otherEnvironment, foreignEnvironment, uuid.NewString(), "not-a-uuid", "env_" + environment, "\xff", "ffffffff-ffff-ffff-ffff-ffffffffffff"} {
		for _, query := range []url.Values{{"environment_id": {filter}}, {"environment_id": {filter}, "after": {published[0]}, "limit": {"1"}}} {
			if status, raw, _ := list(owner, session, query); status != http.StatusOK || raw != empty {
				t.Errorf("environment_id=%q %v: %d %s", filter, query, status, raw)
			}
		}
	}
	// Cursor validation and page bounds keep their own errors under any filter;
	// malformed and unknown cursors share the Artifact cursor error.
	const invalidCursor = `{"error":{"message":"after is not a valid artifact ID","type":"invalid_request_error","code":"invalid_request_error","param":null}}` + "\n"
	for _, query := range []url.Values{{"environment_id": {"not-a-uuid"}, "after": {"not-a-uuid"}}, {"environment_id": {"not-a-uuid"}, "after": {uuid.NewString()}}} {
		if status, raw, _ := list(owner, session, query); status != http.StatusBadRequest || raw != invalidCursor {
			t.Errorf("%v: %d %s", query, status, raw)
		}
	}
	if status, raw, _ := list(owner, session, url.Values{"environment_id": {"not-a-uuid"}, "limit": {"0"}}); status != http.StatusBadRequest || raw == invalidCursor {
		t.Errorf("limit 0 with malformed filter: %d %s", status, raw)
	}

	// Tenant and Session scoping precede the filter: foreign and missing Sessions stay 404.
	missingStatus, missingBody := client.do(owner, http.MethodGet, "/v1/agents/sessions/"+uuid.NewString()+"/artifacts", "", nil)
	if missingStatus != http.StatusNotFound {
		t.Fatalf("missing Session: %d %s", missingStatus, missingBody)
	}
	for _, probe := range []struct{ token, session string }{{foreign, session}, {owner, foreignSession}, {owner, uuid.NewString()}, {owner, "not-a-uuid"}} {
		for _, filter := range []string{"", "not-a-uuid", environment, foreignEnvironment} {
			query := url.Values{}
			if filter != "" {
				query.Set("environment_id", filter)
			}
			if status, raw, _ := list(probe.token, probe.session, query); status != missingStatus || raw != missingBody {
				t.Errorf("foreign or missing Session %s with filter %q: %d %s", probe.session, filter, status, raw)
			}
		}
	}
	// The foreign tenant still sees only its own Artifacts.
	if status, raw, own := list(foreign, foreignSession, nil); status != http.StatusOK || len(own.Data) != 1 {
		t.Fatalf("foreign tenant own list: %d %s", status, raw)
	}
}

// The pinned SDK and raw HTTP verifier used by live acceptance reads the
// republication result of three Turns, the list envelope and the filter.
func TestSessionArtifactsOfficialClientPostgres(t *testing.T) {
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	s, _ := store.NewTestStore(t)
	server, owner, ownerTenant, foreign, _ := artifactHTTPServer(t, s)
	session, environment := hostedArtifactSession(t, s, ownerTenant, "artifact-sdk")
	outputs := map[string]string{"a.txt": "alpha", "sub/b.txt": "bravo", "empty.txt": ""}
	first := completeArtifactTurn(t, s, ownerTenant, session, environment, "artifact-sdk-1", outputs)
	page, err := s.ListSessionArtifacts(t.Context(), ownerTenant, session, "", "", 100, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range page.Artifacts {
		if artifact.Path == "/workspace/outputs/a.txt" {
			if err := s.DeleteSessionArtifact(t.Context(), ownerTenant, session, artifact.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	outputs["c.txt"] = "charlie"
	second := completeArtifactTurn(t, s, ownerTenant, session, environment, "artifact-sdk-2", outputs)
	outputs["sub/b.txt"] = "bravo-v2"
	third := completeArtifactTurn(t, s, ownerTenant, session, environment, "artifact-sdk-3", outputs)
	hex := func(files map[string]string) map[string]string {
		out := make(map[string]string, len(files))
		for name, body := range files {
			out["/workspace/outputs/"+name] = fmt.Sprintf("%x", body)
		}
		return out
	}
	settings, err := json.Marshal(map[string]any{"base": server.URL, "token": owner, "foreign": foreign, "session": session, "environment": environment,
		"expected": map[string]map[string]string{
			first:  hex(map[string]string{"sub/b.txt": "bravo", "empty.txt": ""}),
			second: hex(map[string]string{"a.txt": "alpha", "c.txt": "charlie"}),
			third:  hex(map[string]string{"sub/b.txt": "bravo-v2"}),
		}})
	if err != nil {
		t.Fatal(err)
	}
	const driver = `import json, sys
sys.path.insert(0, "../../tests")
import httpx2
from openai import DefaultHttpxClient, OpenAI
from official_session_artifacts import verify_session_artifacts
s = json.load(sys.stdin)
def client(key):
    return OpenAI(api_key=key, base_url=s["base"] + "/v1", max_retries=0,
                  _strict_response_validation=True, http_client=DefaultHttpxClient(trust_env=False))
expected = {turn: {path: bytes.fromhex(body) for path, body in files.items()} for turn, files in s["expected"].items()}
with httpx2.Client(trust_env=False, timeout=20) as http, client(s["token"]) as owner, client(s["foreign"]) as foreign:
    items = verify_session_artifacts(owner, foreign, http, s["session"], s["environment"], expected)
print(json.dumps({"verified_artifacts": len(items)}))
`
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-c", driver)
	command.Stdin = bytes.NewReader(settings)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), `{"verified_artifacts": 5}`) {
		t.Fatalf("official Artifact verification: %v %s", err, output)
	}
}
