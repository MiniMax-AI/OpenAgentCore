package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	routingKey        = "routing-project-key"
	routingDerivedKey = "routing-derived-key"
	routingAdminKey   = "routing-admin-key"
	unauthorizedV1    = `{"error":{"message":"A valid Agents API bearer key is required.","type":"invalid_request_error","code":null,"param":null}}` + "\n"
	invalidBetaV1     = `{"error":{"message":"To access the Agents API, set the 'OpenAI-Beta' header to 'agents=v1'.","type":"invalid_beta","code":"invalid_beta","param":null}}` + "\n"
	notAllowedV1      = `{"error":{"message":"This API method is not supported.","type":"invalid_request_error","code":"unsupported_operation","param":null}}` + "\n"
)

var requestIDPattern = regexp.MustCompile(`^req_[0-9a-f]{32}$`)

// routingStore serves one saved Agent and records Agent lookups.
type routingStore struct {
	tenant           string
	agent            agents.Agent
	lookups, updates []string
}

func (s *routingStore) GetAgent(_ context.Context, tenant, id string) (agents.Agent, error) {
	s.lookups = append(s.lookups, id)
	if tenant != s.tenant || id != s.agent.ID {
		return agents.Agent{}, agents.ErrNotFound
	}
	return s.agent, nil
}

func (s *routingStore) ListAgents(_ context.Context, query agents.ListQuery) (agents.Page, error) {
	if query.TenantID != s.tenant {
		return agents.Page{}, nil
	}
	return agents.Page{Agents: []agents.Agent{s.agent}}, nil
}

func (s *routingStore) Update(_ context.Context, command agents.UpdateCommand) (agents.Agent, error) {
	s.updates = append(s.updates, command.AgentID)
	if command.TenantID != s.tenant || command.AgentID != s.agent.ID {
		return agents.Agent{}, agents.ErrNotFound
	}
	if command.Metadata != nil {
		s.agent.Metadata = *command.Metadata
	}
	return s.agent, nil
}

// trapTB turns an unexpected call to a strict fake into a panic without failing
// the test, which outcome reports as "handler reached".
type trapTB struct{ testing.TB }

func (trapTB) Errorf(format string, args ...any) { panic(fmt.Sprintf(format, args...)) }

// routingFixture returns the served handler and, for route enumeration, a
// router built from the same Dependencies. They answer only Agent reads and
// updates, Project key resolution, including a derived key for the same
// Project, and File lookups, which report every File as missing. Any other
// call, including sandbox administration and Project key management, panics
// if a handler is ever reached.
func routingFixture(t *testing.T) (http.Handler, *chi.Mux, *routingStore) {
	t.Helper()
	tenant := uuid.NewString()
	keys := projectKeys(t, APIKey{OrganizationID: "test-org", ProjectID: "test-project", SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential(routingKey), TenantID: tenant})
	keys[runtimedevice.HashCredential(routingDerivedKey)] = keys[runtimedevice.HashCredential(routingKey)]
	s := &routingStore{tenant: tenant, agent: agents.Agent{ID: uuid.NewString(), TenantID: tenant, Metadata: map[string]string{},
		Configuration: json.RawMessage(`{"model":"fixture"}`), CreatedAt: time.Unix(1700000000, 0), UpdatedAt: time.Unix(1700000000, 0)}}
	deps, fakes := testDependencies(trapTB{t})
	fakes.projects.resolveProjectAPIKey = keys.ResolveProjectAPIKey
	fakes.agentsReader.getAgent, fakes.agentsReader.listAgents, fakes.agents.update = s.GetAgent, s.ListAgents, s.Update
	fakes.files.getSourceFile = func(context.Context, string, string) (store.SourceFile, error) {
		return store.SourceFile{}, store.ErrNotFound
	}
	deps.CoreKeys = coreKeys(t, routingAdminKey)
	deps.Execution, deps.Sandboxes = fakes.execution(), fakes.sandboxes()
	return newTestHandler(t, deps), (&Handler{Dependencies: deps}).routes(), s
}

func routingHeaders(pairs ...string) http.Header {
	header := http.Header{}
	for i := 0; i < len(pairs); i += 2 {
		header.Add(pairs[i], pairs[i+1])
	}
	return header
}

// project and beta are the pinned SDK's headers.
var (
	project  = []string{"Authorization", "Bearer " + routingKey}
	beta     = []string{"OpenAI-Beta", "agents=v1"}
	jsonBody = []string{"Content-Type", "application/json"}
)

func withHeaders(parts ...[]string) http.Header {
	var pairs []string
	for _, part := range parts {
		pairs = append(pairs, part...)
	}
	return routingHeaders(pairs...)
}

func serve(handler http.Handler, method, target, body string, header http.Header) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	for name, values := range header {
		request.Header[name] = values
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func sameResponse(a, b *httptest.ResponseRecorder) bool {
	return a.Code == b.Code && a.Body.String() == b.Body.String() &&
		a.Header().Get("Allow") == b.Header().Get("Allow") && a.Header().Get("WWW-Authenticate") == b.Header().Get("WWW-Authenticate") &&
		a.Header().Get("Content-Type") == b.Header().Get("Content-Type")
}

// The canonical path decodes only unreserved escapes, then resolves empty and
// dot segments with ServeMux semantics, keeping a trailing slash (HP-17/HP-18).
func TestCanonicalPathsRewriteBeforeRouting(t *testing.T) {
	for _, test := range []struct{ target, path, rawPath, query string }{
		{"/v1//agents/a", "/v1/agents/a", "", ""},
		{"//v1/agents?limit=1", "/v1/agents", "", "limit=1"},
		{"/v1/agents/x/../y", "/v1/agents/y", "", ""},
		{"/v1/./agents/.", "/v1/agents", "", ""},
		{"/v1/agents/", "/v1/agents/", "", ""},
		{"/v1/agents/x/../", "/v1/agents/", "", ""},
		{"/v1/agents/agent%5Fid%2d%7E", "/v1/agents/agent_id-~", "", ""},
		{"/v1/agents/%2E%2E/%2e%2E/core", "/core", "", ""},
		{"/v1/agents/.%2E/x", "/v1/x", "", ""},
		{"/v1/agents/a%2Fb", "/v1/agents/a/b", "/v1/agents/a%2Fb", ""},
		{"/v1/agents/..%2F..%2Fcore", "/v1/agents/../../core", "/v1/agents/..%2F..%2Fcore", ""},
		{"/v1/agents/a%20b", "/v1/agents/a b", "", ""},
		{"/v1/agents/%25", "/v1/agents/%", "", ""},
	} {
		var seen *http.Request
		handler := CanonicalPaths(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r }))
		request := httptest.NewRequest(http.MethodPost, test.target, nil)
		handler.ServeHTTP(httptest.NewRecorder(), request)
		if seen.URL.Path != test.path || seen.URL.RawPath != test.rawPath || seen.URL.RawQuery != test.query || seen.Method != http.MethodPost {
			t.Errorf("%s: path %q raw %q query %q", test.target, seen.URL.Path, seen.URL.RawPath, seen.URL.RawQuery)
		}
		again := httptest.NewRequest(http.MethodGet, seen.URL.RequestURI(), nil)
		var repeated *http.Request
		CanonicalPaths(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { repeated = r })).ServeHTTP(httptest.NewRecorder(), again)
		if repeated.URL.EscapedPath() != seen.URL.EscapedPath() {
			t.Errorf("%s: canonicalization is not idempotent: %q", test.target, repeated.URL.EscapedPath())
		}
	}
}

// Non-canonical and encoded paths are served, never redirected, and reach the
// same resource as the clean path: an update through // applies (HP-17), and a
// percent-encoded unreserved ID resolves (HP-18). Malformed IDs keep the 404.
func TestNonCanonicalPathsServeTheCleanRoute(t *testing.T) {
	handler, _, s := routingFixture(t)
	id := s.agent.ID
	authenticated := withHeaders(project, beta)
	clean := serve(handler, http.MethodGet, "/v1/agents/"+id, "", authenticated)
	if clean.Code != http.StatusOK {
		t.Fatalf("clean retrieve = %d %s", clean.Code, clean.Body)
	}
	for _, target := range []string{"/v1//agents/" + id, "/v1/agents//" + id, "//v1/agents/" + id, "/v1/agents/x/../" + id, "/v1/./agents/" + id,
		"/v1/agents/" + strings.ReplaceAll(id, "-", "%2D"), "/v1/agents/" + strings.Replace(id, "-", "%2d", 1), "/%76%31/agents/" + id,
		"/v1/agents/%2E%2E/agents/" + id} {
		if got := serve(handler, http.MethodGet, target, "", authenticated); !sameResponse(got, clean) || got.Header().Get("Location") != "" {
			t.Errorf("%s = %d %s", target, got.Code, got.Body)
		}
	}
	for _, lookup := range s.lookups {
		if lookup != id {
			t.Fatalf("handler saw a non-canonical ID %q", lookup)
		}
	}
	// A derived project API key authenticates the canonical route like its parent.
	derived := withHeaders([]string{"Authorization", "Bearer " + routingDerivedKey}, beta)
	if got := serve(handler, http.MethodGet, "/v1//agents/%2e/"+id, "", derived); !sameResponse(got, clean) {
		t.Fatalf("derived key through // = %d %s", got.Code, got.Body)
	}
	list := serve(handler, http.MethodGet, "/v1/agents", "", authenticated)
	if got := serve(handler, http.MethodGet, "/v1//agents", "", authenticated); list.Code != http.StatusOK || !sameResponse(got, list) {
		t.Fatalf("list through // = %d %s", got.Code, got.Body)
	}
	posted := withHeaders(project, beta, jsonBody)
	updated := serve(handler, http.MethodPost, "/v1//agents/"+id, `{"metadata":{"route":"double-slash"}}`, posted)
	if updated.Code != http.StatusOK || s.agent.Metadata["route"] != "double-slash" || len(s.updates) != 1 || s.updates[0] != id {
		t.Fatalf("update through // = %d %s, updates %v", updated.Code, updated.Body, s.updates)
	}
	created := serve(handler, http.MethodPost, "/v1//agents", `{}`, posted)
	if want := serve(handler, http.MethodPost, "/v1/agents", `{}`, posted); created.Code != http.StatusBadRequest || !sameResponse(created, want) {
		t.Fatalf("create through // = %d %s", created.Code, created.Body)
	}
	missing := serve(handler, http.MethodGet, "/v1/agents/"+uuid.NewString(), "", authenticated)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), `"code":"not_found_error"`) {
		t.Fatalf("missing = %d %s", missing.Code, missing.Body)
	}
	for _, target := range []string{"/v1/agents/x/../" + uuid.NewString(), "/v1/agents/" + strings.Replace(id, "-", "%2F", 1), "/v1/agents/%2D", "/v1/agents/" + id[:8] + "%00"} {
		if got := serve(handler, http.MethodGet, target, "", authenticated); !sameResponse(got, missing) {
			t.Errorf("%s = %d %s", target, got.Code, got.Body)
		}
	}
	// Trailing slashes and unknown sub-routes keep the Beta group's 404 (R11).
	unknown := serve(handler, http.MethodGet, "/v1/agents/"+id+"/unknown", "", authenticated)
	for _, target := range []string{"/v1/agents/", "/v1/agents/" + id + "/", "/v1/agents/x/../", "/v1/agents/" + id + "/unknown", "/v1/agents//" + id + "/unknown"} {
		got := serve(handler, http.MethodGet, target, "", authenticated)
		if got.Code != http.StatusNotFound || !sameResponse(got, unknown) || !strings.Contains(got.Body.String(), `"code":"unsupported_operation"`) {
			t.Errorf("%s = %d %s", target, got.Code, got.Body)
		}
	}
	if got := serve(handler, http.MethodPost, "/v1/agents/", `{"model":"fixture"}`, withHeaders(project, beta, jsonBody)); got.Code != http.StatusNotFound {
		t.Fatalf("trailing-slash create = %d %s", got.Code, got.Body)
	}
}

func concretePath(pattern string) string {
	parts := strings.Split(pattern, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, "{") || part == "*" {
			parts[i] = uuid.NewString()
		}
	}
	return strings.Join(parts, "/")
}

// dirtyVariants spells a clean path with empty, dot and encoded segments,
// including traversal from other route groups.
func dirtyVariants(clean string) []string {
	first, rest, _ := strings.Cut(strings.TrimPrefix(clean, "/"), "/")
	if rest != "" {
		rest = "/" + rest
	}
	return []string{
		"/" + clean,
		"/" + first + "//" + strings.TrimPrefix(rest, "/"),
		"/" + first + "/." + rest,
		"/" + first + "/zz/.." + rest,
		"/" + first + "/zz/%2E%2E" + rest,
		"/" + first + "/%2e" + rest,
		"/%" + strings.ToUpper(strconv.FormatInt(int64(first[0]), 16)) + first[1:] + rest,
		"/api/v1/agent-daemon/%2E%2E/%2E%2E/%2E%2E" + clean,
		"/core/v1/sandbox/../../.." + clean,
		"/v1/agents/%2E%2E/%2e%2e" + clean,
	}
}

// Security regression for path canonicalization (R1/R2) and Beta ordering
// (R6): every route except the explicitly self-authenticated ones rejects an
// unauthenticated request before any handler, and a dirty or encoded spelling
// of its path gives exactly the response of the clean path for every
// credential, so it can never reach another route group or skip its checks.
func TestEveryRouteAuthenticatesItsCanonicalPath(t *testing.T) {
	handler, router, s := routingFixture(t)
	selfAuthenticated := map[string]bool{"GET /healthz": false, "POST /api/v1/sandbox-node/enroll": false, "GET /api/v1/sandbox-node/identity": false, "GET /api/v1/sandbox-node/configuration": false}
	credentials := []http.Header{{}, withHeaders(beta), withHeaders([]string{"Authorization", "Bearer " + routingAdminKey}, beta),
		withHeaders([]string{"Authorization", "Basic " + routingKey}, beta), withHeaders([]string{"Authorization", "Bearer wrong"}),
		withHeaders(project), withHeaders(project, []string{"OpenAI-Beta", "agents=v0"}),
		withHeaders([]string{"Authorization", "Bearer " + routingDerivedKey}), withHeaders([]string{"Authorization", "Bearer " + routingDerivedKey}, beta)}
	routes := 0
	walked := map[string]bool{}
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		walked[method+" "+route] = true
		if _, ok := selfAuthenticated[method+" "+route]; ok {
			selfAuthenticated[method+" "+route] = true
			return nil
		}
		routes++
		clean := concretePath(route)
		admin := strings.HasPrefix(route, "/core/v1/")
		betaGroup := strings.HasPrefix(route, "/v1/") && !strings.HasPrefix(route, "/v1/files") && !strings.HasPrefix(route, "/v1/skills")
		for _, header := range credentials {
			projectKey := header.Get("Authorization") == "Bearer "+routingKey || header.Get("Authorization") == "Bearer "+routingDerivedKey
			betaHeader := header.Get("OpenAI-Beta") == "agents=v1"
			adminKey := header.Get("Authorization") == "Bearer "+routingAdminKey
			if admin && adminKey || !admin && projectKey && (betaHeader || !betaGroup) {
				continue // Authenticated requests reach handlers.
			}
			want := serve(handler, method, clean, "", header)
			if betaGroup && !betaHeader {
				if want.Code != http.StatusBadRequest || want.Body.String() != invalidBetaV1 {
					t.Errorf("%s %s without Beta = %d %s", method, clean, want.Code, want.Body)
				}
			} else if want.Code != http.StatusUnauthorized || want.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Errorf("%s %s unauthenticated = %d %s", method, clean, want.Code, want.Body)
			}
			for _, variant := range dirtyVariants(clean) {
				if got := serve(handler, method, variant, "", header); !sameResponse(got, want) {
					t.Errorf("%s %s = %d %s; clean path gives %d %s", method, variant, got.Code, got.Body, want.Code, want.Body)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"GET /core/v1/projects", "POST /core/v1/projects",
		"DELETE /core/v1/projects/{project_id}/keys/{key_id}", "GET /core/v1/sandbox/nodes", "DELETE /core/v1/projects/{project_id}/environments/{environment_id}/executor-credentials/{key_id}"} {
		if !walked[route] {
			t.Errorf("route %s was not walked", route)
		}
	}
	for route, found := range selfAuthenticated {
		if !found {
			t.Errorf("self-authenticated route %s is no longer registered", route)
		}
	}
	// /v1 serves exactly the pinned official method and path set.
	var pinned struct {
		Routes []string `json:"routes"`
	}
	raw, err := os.ReadFile("../../../../contracts/agents-api/upstream-routes.json")
	if err != nil || json.Unmarshal(raw, &pinned) != nil || len(pinned.Routes) == 0 {
		t.Fatal("read pinned routes", err)
	}
	served := []string{}
	for route := range walked {
		method, path, _ := strings.Cut(route, " ")
		if method != http.MethodHead && strings.HasPrefix(path, "/v1/") {
			served = append(served, method+" "+regexp.MustCompile(`\{[^}]*\}`).ReplaceAllString(strings.TrimPrefix(path, "/v1"), "{}"))
		}
	}
	slices.Sort(served)
	slices.Sort(pinned.Routes)
	if !slices.Equal(served, pinned.Routes) {
		t.Errorf("/v1 routes differ from the pinned set:\nserved %v\npinned %v", served, pinned.Routes)
	}
	if routes < 73 || len(s.lookups) != 0 || len(s.updates) != 0 {
		t.Fatalf("walked %d routes; store lookups %v updates %v", routes, s.lookups, s.updates)
	}
	// Traversal into internal groups keeps their own authentication.
	for _, test := range []struct {
		target string
		header http.Header
		status int
		code   string
	}{
		{"/v1/%2E%2E/core/v1/sandbox/nodes", withHeaders(project, beta), http.StatusUnauthorized, "invalid_admin_key"},
		{"/v1/agents/%2e%2e/%2e%2e/core/v1/sandbox/deployment", withHeaders(project, beta), http.StatusUnauthorized, "invalid_admin_key"},
		{"/v1/agents/..%2F..%2Fcore/v1/sandbox/nodes", withHeaders(project, beta), http.StatusNotFound, "unsupported_operation"},
		{"/core/v1/sandbox/%2E%2E/%2E%2E/%2E%2E/v1/agents", withHeaders([]string{"Authorization", "Bearer " + routingAdminKey}, beta), http.StatusUnauthorized, ""},
		{"/core/v1/sandbox/nodes%2F..%2F..%2F..%2Fv1%2Fagents", withHeaders([]string{"Authorization", "Bearer " + routingAdminKey}, beta), http.StatusNotFound, ""},
		// Project API key management keeps deployment administrator authority.
		{"/v1/%2E%2E/core/v1/projects/" + runtimedevice.HashCredential(routingKey), withHeaders(project, beta), http.StatusUnauthorized, "invalid_admin_key"},
		{"/v1/agents//../../core/v1/projects/" + runtimedevice.HashCredential(routingKey), withHeaders([]string{"Authorization", "Bearer " + routingDerivedKey}, beta), http.StatusUnauthorized, "invalid_admin_key"},
		{"/v1/x{/..%2F..%2Fcore/v1/project-api-keys/" + runtimedevice.HashCredential(routingKey), http.Header{}, http.StatusBadRequest, "invalid_beta"},
		{"/core/v1/projects/" + runtimedevice.HashCredential(routingKey) + "/%2E%2E/%2E%2E/%2E%2E/%2E%2E/%2E%2E/v1/agents", withHeaders([]string{"Authorization", "Bearer " + routingAdminKey}, beta), http.StatusUnauthorized, ""},
	} {
		got := serve(handler, http.MethodGet, test.target, "", test.header)
		if got.Code != test.status || (test.code != "" && !strings.Contains(got.Body.String(), `"code":"`+test.code+`"`)) {
			t.Errorf("%s = %d %s", test.target, got.Code, got.Body)
		}
	}
}

// Beta is checked first, and only one exact field value is accepted
// (HP-02/03/05, R11 agents=v0). Files and Skills ignore the header.
func TestOpenAIBetaHeaderPrecedesAuthentication(t *testing.T) {
	handler, _, s := routingFixture(t)
	for _, test := range []struct {
		name   string
		header http.Header
		status int
	}{
		{"exact", withHeaders(project, beta), http.StatusOK},
		{"missing without credentials", http.Header{}, http.StatusBadRequest},
		{"missing with invalid bearer", routingHeaders("Authorization", "Bearer wrong"), http.StatusBadRequest},
		{"missing with valid bearer", withHeaders(project), http.StatusBadRequest},
		{"two lines", withHeaders(project, beta, []string{"OpenAI-Beta", "assistants=v2"}), http.StatusBadRequest},
		{"two lines without credentials", withHeaders(beta, []string{"OpenAI-Beta", "assistants=v2"}), http.StatusBadRequest},
		{"repeated exact line", withHeaders(project, beta, beta), http.StatusBadRequest},
		{"combined", withHeaders(project, []string{"OpenAI-Beta", "agents=v1, assistants=v2"}), http.StatusBadRequest},
		{"agents=v0", withHeaders(project, []string{"OpenAI-Beta", "agents=v0"}), http.StatusBadRequest},
		{"case", withHeaders(project, []string{"OpenAI-Beta", "Agents=v1"}), http.StatusBadRequest},
		{"empty", withHeaders(project, []string{"OpenAI-Beta", ""}), http.StatusBadRequest},
		{"beta without credentials", withHeaders(beta), http.StatusUnauthorized},
	} {
		got := serve(handler, http.MethodGet, "/v1/agents/"+s.agent.ID, "", test.header)
		if got.Code != test.status || (test.status == http.StatusBadRequest && got.Body.String() != invalidBetaV1) {
			t.Errorf("%s = %d %s", test.name, got.Code, got.Body)
		}
	}
	for _, header := range []http.Header{withHeaders(project), withHeaders(project, []string{"OpenAI-Beta", "foo=bar"})} {
		if got := serve(handler, http.MethodGet, "/v1/files/file-missing", "", header); got.Code != http.StatusNotFound {
			t.Errorf("Files with Beta %q = %d %s", header.Get("OpenAI-Beta"), got.Code, got.Body)
		}
	}
	if got := serve(handler, http.MethodGet, "/v1/files/file-missing", "", http.Header{}); got.Code != http.StatusUnauthorized {
		t.Errorf("Files without credentials = %d %s", got.Code, got.Body)
	}
}

// Every 401 is invalid_request_error (HP-07). Beta 401s have a null code;
// Files and Skills report invalid_api_key only for a rejected Bearer
// credential. WWW-Authenticate and the message are unchanged.
func TestUnauthorizedEnvelopes(t *testing.T) {
	handler, _, s := routingFixture(t)
	invalidKey := strings.Replace(unauthorizedV1, `"code":null`, `"code":"invalid_api_key"`, 1)
	for _, test := range []struct {
		name          string
		authorization []string
		scope         []string
		project       string
	}{
		{"missing", nil, nil, unauthorizedV1},
		{"basic", []string{"Basic " + routingKey}, nil, unauthorizedV1},
		{"empty bearer", []string{"Bearer"}, nil, unauthorizedV1},
		{"duplicate", []string{"Bearer " + routingKey, "Bearer " + routingKey}, nil, unauthorizedV1},
		{"invalid bearer", []string{"Bearer wrong"}, nil, invalidKey},
		{"wrong project", []string{"Bearer " + routingKey}, []string{"OpenAI-Project", "other"}, invalidKey},
	} {
		header := withHeaders(beta, test.scope)
		for _, value := range test.authorization {
			header.Add("Authorization", value)
		}
		for _, route := range []struct{ method, path, want string }{
			{http.MethodGet, "/v1/agents/" + s.agent.ID, unauthorizedV1},
			{http.MethodGet, "/v1/files/file-missing", test.project},
			{http.MethodGet, "/v1/skills/skill-missing", test.project},
		} {
			got := serve(handler, route.method, route.path, "", header)
			if got.Code != http.StatusUnauthorized || got.Body.String() != route.want || got.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Errorf("%s %s = %d %s", test.name, route.path, got.Code, got.Body)
			}
		}
	}
	got := serve(handler, http.MethodGet, "/core/v1/sandbox/nodes", "", http.Header{})
	if got.Code != http.StatusUnauthorized || !strings.Contains(got.Body.String(), `"type":"invalid_request_error","code":"invalid_admin_key"`) {
		t.Fatalf("administrator 401 = %d %s", got.Code, got.Body)
	}
}

// A 405 keeps Core's JSON body and lists the route's methods (HP-20). Beta and
// authentication run first.
func TestMethodNotAllowedListsRouteMethods(t *testing.T) {
	handler, _, s := routingFixture(t)
	session := uuid.NewString()
	for _, test := range []struct{ method, path, allow string }{
		{http.MethodPut, "/v1/agents/" + s.agent.ID, "GET,HEAD,POST,DELETE"},
		{http.MethodPatch, "/v1/agents/" + s.agent.ID, "GET,HEAD,POST,DELETE"},
		{http.MethodDelete, "/v1/agents", "GET,HEAD,POST"},
		{http.MethodPut, "/v1/agents/sessions/" + session + "/events", "GET,POST"},
		{http.MethodPost, "/v1/agents/sessions/" + session + "/turns", "GET,HEAD"},
		{http.MethodPost, "/v1/agents/sessions/" + session + "/artifacts/" + uuid.NewString() + "/content", "GET"},
		{http.MethodPut, "/v1//agents/" + s.agent.ID, "GET,HEAD,POST,DELETE"},
		// Wrong methods on Files and Skills paths reach the Beta group, as before.
		{http.MethodPut, "/v1/files/file-missing", "GET,HEAD,DELETE"},
	} {
		got := serve(handler, test.method, test.path, "", withHeaders(project, beta))
		if got.Code != http.StatusMethodNotAllowed || got.Body.String() != notAllowedV1 || got.Header().Get("Allow") != test.allow {
			t.Errorf("%s %s = %d %q %s", test.method, test.path, got.Code, got.Header().Get("Allow"), got.Body)
		}
	}
	// Routes outside the Beta group and unknown methods use the same 405.
	executor := "/core/v1/projects/" + uuid.NewString() + "/environments/" + uuid.NewString() + "/executor-credentials"
	core := withHeaders([]string{"Authorization", "Bearer " + routingAdminKey})
	for _, test := range []struct {
		method, path, allow string
		header              http.Header
	}{
		{http.MethodPost, "/healthz", "GET,HEAD", nil},
		{http.MethodPut, executor, "GET,HEAD,POST", core},
		{http.MethodGet, executor + "/" + uuid.NewString(), "DELETE", core},
		{"FOO", "/v1/agents", "GET,HEAD,POST", nil},
		{"FOO", "/v1//agents/" + s.agent.ID, "GET,HEAD,POST,DELETE", nil},
	} {
		got := serve(handler, test.method, test.path, "", test.header)
		if got.Code != http.StatusMethodNotAllowed || got.Body.String() != notAllowedV1 || got.Header().Get("Allow") != test.allow {
			t.Errorf("%s %s = %d %q %s", test.method, test.path, got.Code, got.Header().Get("Allow"), got.Body)
		}
	}
	if got := serve(handler, http.MethodPut, executor, "", nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated executor 405 = %d %s", got.Code, got.Body)
	}
	if got := serve(handler, http.MethodPut, "/v1/agents/"+s.agent.ID, "", withHeaders(beta)); got.Code != http.StatusUnauthorized || got.Header().Get("Allow") != "" {
		t.Fatalf("unauthenticated 405 = %d %s", got.Code, got.Body)
	}
	if got := serve(handler, http.MethodPut, "/v1/agents/"+s.agent.ID, "", withHeaders(project)); got.Code != http.StatusBadRequest {
		t.Fatalf("Beta-less 405 = %d %s", got.Code, got.Body)
	}
}

// HEAD runs a GET route without a body after the same checks (HP-19). SSE,
// content-download, live directory and Runtime observation or history routes
// answer 405 without opening a stream, reading content or doing Runtime work.
func TestHeadRequests(t *testing.T) {
	handler, _, s := routingFixture(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	do := func(method, path string, header http.Header) (*http.Response, []byte) {
		t.Helper()
		request, err := http.NewRequest(method, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header = header
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response, body
	}
	authenticated := withHeaders(project, beta)
	for _, path := range []string{"/v1/agents", "/v1/agents/" + s.agent.ID, "/v1//agents/" + s.agent.ID, "/v1/agents/" + uuid.NewString(), "/v1/files/file-missing", "/healthz"} {
		get, getBody := do(http.MethodGet, path, authenticated)
		head, headBody := do(http.MethodHead, path, authenticated)
		if head.StatusCode != get.StatusCode || len(headBody) != 0 || head.ContentLength != int64(len(getBody)) ||
			head.Header.Get("Content-Type") != get.Header.Get("Content-Type") || !requestIDPattern.MatchString(head.Header.Get("X-Request-Id")) {
			t.Errorf("HEAD %s = %d length %d %q; GET = %d %d", path, head.StatusCode, head.ContentLength, headBody, get.StatusCode, len(getBody))
		}
	}
	for _, test := range []struct {
		header http.Header
		status int
	}{{withHeaders(project), http.StatusBadRequest}, {withHeaders(beta), http.StatusUnauthorized}} {
		if head, _ := do(http.MethodHead, "/v1/agents", test.header); head.StatusCode != test.status {
			t.Errorf("HEAD checks = %d, want %d", head.StatusCode, test.status)
		}
	}
	session := "/v1/agents/sessions/" + uuid.NewString()
	for _, test := range []struct{ path, allow string }{
		{session + "/events", "GET,POST"},
		{"/v1/agents/environments/" + uuid.NewString() + "/files", "GET,POST"},
		{session + "/artifacts/" + uuid.NewString() + "/content", "GET"},
		{"/v1/files/file-missing/content", "GET"},
		{"/v1/skills/skill-missing/content", "GET"},
		{"/v1/skills/skill-missing/versions/1/content", "GET"},
	} {
		head, _ := do(http.MethodHead, test.path, authenticated)
		if head.StatusCode != http.StatusMethodNotAllowed || head.Header.Get("Allow") != test.allow || head.Header.Get("Content-Type") != "application/json" {
			t.Errorf("HEAD %s = %d %q", test.path, head.StatusCode, head.Header.Get("Allow"))
		}
		if head, _ := do(http.MethodHead, test.path, withHeaders(beta)); head.StatusCode != http.StatusUnauthorized {
			t.Errorf("unauthenticated HEAD %s = %d", test.path, head.StatusCode)
		}
	}
}

// Retired Core extensions are unknown /v1 paths; the administrator reads stay
// under /core/v1.
func TestRetiredV1ExtensionsAreNotFound(t *testing.T) {
	handler, _, _ := routingFixture(t)
	session := "/v1/agents/sessions/" + uuid.NewString()
	unknown := serve(handler, http.MethodGet, session+"/unknown", "", withHeaders(project, beta))
	for _, path := range []string{"/v1/agents/core/startup-configuration", "/v1/agents/runtime-history/capabilities",
		session + "/runtime-observation", session + "/runtime-history", session + "/execution-configuration", session + "/sandbox-placement", "/v1/sandbox/nodes"} {
		if got := serve(handler, http.MethodGet, path, "", withHeaders(project, beta)); got.Code != http.StatusNotFound || got.Body.String() != unknown.Body.String() {
			t.Errorf("GET %s = %d %s", path, got.Code, got.Body)
		}
	}
	// This spelling is an Agent ID now.
	if got := serve(handler, http.MethodGet, "/v1/agents/runtime-observations", "", withHeaders(project, beta)); got.Code != http.StatusNotFound {
		t.Errorf("GET /v1/agents/runtime-observations = %d %s", got.Code, got.Body)
	}
}

// Every response carries a fresh request ID and the observed OpenAI headers
// (HP-23/HP-24), next to Core's traceparent and Cache-Control extensions.
func TestAgentsResponseHeaders(t *testing.T) {
	handler, _, s := routingFixture(t)
	seen := map[string]bool{}
	for _, test := range []struct {
		method, path string
		header       http.Header
		status       int
	}{
		{http.MethodGet, "/healthz", nil, http.StatusOK},
		{http.MethodGet, "/v1/agents", withHeaders(project, beta), http.StatusOK},
		{http.MethodGet, "/v1/agents/" + s.agent.ID, withHeaders(project), http.StatusBadRequest},
		{http.MethodGet, "/v1/agents/" + s.agent.ID, withHeaders(beta), http.StatusUnauthorized},
		{http.MethodGet, "/v1/agents/" + uuid.NewString(), withHeaders(project, beta), http.StatusNotFound},
		{http.MethodGet, "/v1/agents/" + s.agent.ID + "/unknown", withHeaders(project, beta), http.StatusNotFound},
		{http.MethodPut, "/v1/agents/" + s.agent.ID, withHeaders(project, beta), http.StatusMethodNotAllowed},
		{http.MethodGet, "/v1/files/file-missing", nil, http.StatusUnauthorized},
		{http.MethodGet, "/core/v1/sandbox/nodes", nil, http.StatusUnauthorized},
		{http.MethodGet, "/unknown", nil, http.StatusNotFound},
	} {
		got := serve(handler, test.method, test.path, "", test.header)
		header := got.Header()
		id := header.Get("X-Request-Id")
		milliseconds, err := strconv.ParseInt(header.Get("Openai-Processing-Ms"), 10, 64)
		if got.Code != test.status || !requestIDPattern.MatchString(id) || seen[id] || header.Get("Openai-Version") != "2020-10-01" ||
			err != nil || milliseconds < 0 || header.Get("X-Content-Type-Options") != "nosniff" || header.Get("Traceparent") == "" ||
			header.Get("Openai-Organization") != "" || header.Get("Openai-Project") != "" {
			t.Errorf("%s %s = %d %v", test.method, test.path, got.Code, header)
		}
		if test.path != "/unknown" && header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s lost Cache-Control", test.method, test.path)
		}
		seen[id] = true
	}
	// The ID is attached to the request log context, and a stream that flushes
	// before writing still reports its processing time.
	var logged string
	stream := responseHeadersWithErrors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logged, _ = log.RequestIDFromContext(r.Context())
		w.Header().Set("Content-Type", "text/event-stream")
		controller := http.NewResponseController(w)
		if err := controller.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
			t.Error(err)
		}
		if err := controller.Flush(); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, ": connected\n\n")
	}), func(string) {})
	server := httptest.NewServer(stream)
	defer server.Close()
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if id := response.Header.Get("X-Request-Id"); !requestIDPattern.MatchString(id) || id != logged || response.Header.Get("Openai-Processing-Ms") == "" {
		t.Fatalf("stream headers %v, logged %q", response.Header, logged)
	}
}

// parseRaw parses a request line exactly as net/http's server does.
func parseRaw(method, target string, header http.Header) (*http.Request, error) {
	request, err := http.ReadRequest(bufio.NewReader(strings.NewReader(method + " " + target + " HTTP/1.1\r\nHost: example.test\r\n\r\n")))
	if err != nil {
		return nil, err
	}
	for name, values := range header {
		request.Header[name] = values
	}
	return request, nil
}

// canonicalTarget returns the path CanonicalPaths routes a request on, after
// checking that chi (RawPath, else Path), the ServeMux (EscapedPath) and the
// decoded Path agree, that the path is clean and that a second pass keeps it.
func canonicalTarget(t *testing.T, request *http.Request) string {
	t.Helper()
	var seen *url.URL
	CanonicalPaths(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r.URL })).ServeHTTP(httptest.NewRecorder(), request)
	escaped := seen.EscapedPath()
	decoded, err := url.PathUnescape(escaped)
	if err != nil || decoded != seen.Path || (seen.RawPath != "" && seen.RawPath != escaped) || cleanPath(escaped) != escaped {
		t.Fatalf("%s: inconsistent canonical URL path %q raw %q escaped %q", request.RequestURI, seen.Path, seen.RawPath, escaped)
	}
	again, err := parseRaw(http.MethodGet, escaped, nil)
	if err != nil {
		t.Fatalf("%s: canonical path %q does not parse: %v", request.RequestURI, escaped, err)
	}
	var repeated *url.URL
	CanonicalPaths(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { repeated = r.URL })).ServeHTTP(httptest.NewRecorder(), again)
	if repeated.EscapedPath() != escaped || repeated.RawPath != seen.RawPath || repeated.Path != seen.Path {
		t.Fatalf("%s: canonicalization is not idempotent: %q", request.RequestURI, repeated.EscapedPath())
	}
	return escaped
}

// outcome summarizes a response; a panic means a trap store was reached.
func outcome(handler http.Handler, request *http.Request) (result string) {
	defer func() {
		if recover() != nil {
			result = "handler reached"
		}
	}()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return fmt.Sprintf("%d %q allow=%q www-authenticate=%q", response.Code, response.Body.String(), response.Header().Get("Allow"), response.Header().Get("WWW-Authenticate"))
}

// Every spelling of a request reaches the route group and authentication of
// its canonical form. Bytes that are invalid in an escaped path must not make
// %2F or %5C a separator (the ServeMux and EscapedPath re-escape the decoded
// Path in that case), in any case of hex digit or under double encoding.
func TestRawPathsReachTheirCanonicalRouteGroup(t *testing.T) {
	handler, _, _ := routingFixture(t)
	for _, test := range []struct {
		target, canonical string
		status            int
	}{
		{"/v1/x{/..%2F..%2Fcore/v1/sandbox/nodes", "/v1/x%7B/..%2F..%2Fcore/v1/sandbox/nodes", http.StatusBadRequest},
		{"/v1/x%7B/..%2F..%2Fcore/v1/sandbox/nodes", "/v1/x%7B/..%2F..%2Fcore/v1/sandbox/nodes", http.StatusBadRequest},
		{"/v1/x\"/..%2f..%2fcore/v1/sandbox/nodes", "/v1/x%22/..%2f..%2fcore/v1/sandbox/nodes", http.StatusBadRequest},
		{"/v1/x\\/..%5C..%5Ccore/v1/sandbox/nodes", "/v1/x%5C/..%5C..%5Ccore/v1/sandbox/nodes", http.StatusBadRequest},
		{"/v1/\xc3\xa9/..%2F..%2Fcore/v1/sandbox/nodes", "/v1/%C3%A9/..%2F..%2Fcore/v1/sandbox/nodes", http.StatusBadRequest},
		{"/v1/x|^`<>/..%252F..%252Fcore/v1/sandbox/nodes", "/v1/x%7C%5E%60%3C%3E/..%252F..%252Fcore/v1/sandbox/nodes", http.StatusBadRequest},
		{"http://example.test/v1/x{/..%2F..%2Fcore/v1/sandbox/nodes", "/v1/x%7B/..%2F..%2Fcore/v1/sandbox/nodes", http.StatusBadRequest},
		{"/0\"%2F", "/0%22%2F", http.StatusNotFound},
		{"/core/v1/sandbox/nodes{%2F..%2F..%2F..%2Fv1/agents", "/core/v1/sandbox/nodes%7B%2F..%2F..%2F..%2Fv1/agents", http.StatusUnauthorized},
		// Literal and unreserved-encoded dot segments do resolve.
		{"/v1/x{/../../core/v1/sandbox/nodes", "/core/v1/sandbox/nodes", http.StatusUnauthorized},
		{"/v1/x{/%2E%2E/%2e%2E/core/v1/sandbox/nodes", "/core/v1/sandbox/nodes", http.StatusUnauthorized},
		{"http://example.test//v1/x{/..//../core/./v1/sandbox/nodes", "/core/v1/sandbox/nodes", http.StatusUnauthorized},
	} {
		request, err := parseRaw(http.MethodGet, test.target, nil)
		if err != nil {
			t.Fatalf("%s: %v", test.target, err)
		}
		if got := canonicalTarget(t, request); got != test.canonical {
			t.Errorf("%s: canonical %q, want %q", test.target, got, test.canonical)
		}
		canonical, _ := parseRaw(http.MethodGet, test.canonical, nil)
		want := outcome(handler, canonical)
		if got := outcome(handler, request); got != want || !strings.HasPrefix(got, strconv.Itoa(test.status)+" ") {
			t.Errorf("%s = %s; canonical form gives %s", test.target, got, want)
		}
	}
	// The same through a real listener, which reads the request line itself.
	server := httptest.NewServer(handler)
	defer server.Close()
	for target, status := range map[string]string{
		"/v1/x{/..%2F..%2Fcore/v1/sandbox/nodes":                "400",
		"/v1/\xe2\x98\x83/..%2F..%2Fcore/v1/sandbox/deployment": "400",
		"/v1/x{/../../core/v1/sandbox/nodes":                    "401",
	} {
		if got := rawStatus(t, server.Listener.Addr().String(), target); got != status {
			t.Errorf("raw %q = %s, want %s", target, got, status)
		}
	}
}

// rawStatus sends one request line over TCP and returns the response status code.
func rawStatus(t *testing.T, address, target string) string {
	t.Helper()
	connection, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := io.WriteString(connection, "GET "+target+" HTTP/1.1\r\nHost: example.test\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	return strconv.Itoa(response.StatusCode)
}

var canonicalPathSeeds = []string{
	"v1/agents", "v1//agents/a", "v1/x{/..%2F..%2Fcore/v1/sandbox/nodes", "v1/x\"/..%2f..%2fcore/v1/sandbox/deployment",
	"v1/x\\/..%5C..%5Ccore/v1/sandbox/nodes", "v1/\xc3\xa9/../../core/v1/sandbox/nodes", "v1/%2E%2E/core/v1/sandbox/nodes",
	"v1/files/..%2F..%2Fv1/skills", "0\"%2F", "core/v1/sandbox/nodes{%2F..%2F..%2F..%2Fv1/agents", "v1/agents/%252F..",
	"api/v1/agent-daemon/%2E%2E/%2E%2E/%2E%2E/v1/agents", "v1/x{/%2e./api/v1/sandbox-node/identity", "v1/agents/%7E%5F%2D%41",
	"core/v1/projects/x/environments/y/executor-credentials/%2E%2E/%2E%2E/%2E%2E/%2E%2E/%2E%2E/%2E%2E/%2E%2E/v1/agents",
	"v1/x{/..%2F..%2Fcore/v1/project-api-keys/x", "core/v1/project-api-keys/x/%2E%2E/%2E%2E/%2E%2E/%2E%2E/v1/agents",
	"v1/%2E%2E/core/v1/projects/x/", "core/v1/project-api-keys//x/y",
}

// Differential property over arbitrary request paths: the routed outcome for
// no credentials, the Beta header, the administrator key and a derived project
// API key equals that of the canonical form, and every layer sees one
// consistent canonical path.
func FuzzCanonicalPathsRouteLikeTheirCanonicalForm(f *testing.F) {
	for _, seed := range canonicalPathSeeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, path string) {
		if strings.ContainsAny(path, " ?#") || len(path) > 512 {
			t.Skip()
		}
		segments, trailing, ok := oraclePath("/" + path)
		if !ok {
			t.Skip()
		}
		handler, _, _ := routingFixture(t)
		for i, header := range []http.Header{{}, withHeaders(beta), withHeaders([]string{"Authorization", "Bearer " + routingAdminKey}, beta),
			withHeaders([]string{"Authorization", "Bearer " + routingDerivedKey}, beta)} {
			request, err := parseRaw(http.MethodGet, "/"+path, header)
			if err != nil {
				t.Skip()
			}
			target := canonicalTarget(t, request)
			if got, gotTrailing := routedSegments(target); !slices.Equal(got, segments) || gotTrailing != trailing {
				t.Fatalf("%q routes on %q %v; the oracle gives %q %v", path, got, gotTrailing, segments, trailing)
			}
			canonical, err := parseRaw(http.MethodGet, target, header)
			if err != nil {
				t.Fatal(err)
			}
			got := outcome(handler, request)
			if want := outcome(handler, canonical); got != want {
				t.Fatalf("%q = %s; canonical %q gives %s", path, got, canonical.RequestURI, want)
			}
			// Requests that never reach a Beta handler must also match the
			// oracle's own spelling, independently of CanonicalPaths.
			if i < 3 {
				oracle, err := parseRaw(http.MethodGet, oracleTarget(segments, trailing), header)
				if err != nil {
					t.Fatal(err)
				}
				if want := outcome(handler, oracle); got != want {
					t.Fatalf("%q = %s; oracle %q gives %s", path, got, oracle.RequestURI, want)
				}
			}
		}
	})
}

// oraclePath derives the canonical segments of a raw request path without the
// implementation: split only on a literal '/', decode each segment once, treat
// a segment as a dot segment only if it decodes to "." or "..", and drop empty
// segments. Encoded separators such as %2F and %5C therefore stay inside their
// segment. It reports whether a trailing slash remains and whether every
// segment is validly escaped.
func oraclePath(raw string) (segments []string, trailing, ok bool) {
	for _, part := range strings.Split(raw, "/") {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return nil, false, false
		}
		switch decoded {
		case "", ".":
		case "..":
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
		default:
			segments = append(segments, decoded)
		}
	}
	return segments, strings.HasSuffix(raw, "/") && len(segments) > 0, true
}

// routedSegments splits a routed escaped path on literal '/' and decodes each
// segment once, so hex case does not affect the comparison.
func routedSegments(escaped string) (segments []string, trailing bool) {
	trimmed := strings.TrimPrefix(escaped, "/")
	trailing = strings.HasSuffix(trimmed, "/")
	if trimmed = strings.TrimSuffix(trimmed, "/"); trimmed == "" {
		return nil, false
	}
	for _, part := range strings.Split(trimmed, "/") {
		decoded, _ := url.PathUnescape(part)
		segments = append(segments, decoded)
	}
	return segments, trailing
}

// oracleTarget spells oracle segments as a request path, escaping '%', '/'
// and every byte outside RFC 3986 pchar.
func oracleTarget(segments []string, trailing bool) string {
	var target strings.Builder
	for _, segment := range segments {
		target.WriteByte('/')
		for i := 0; i < len(segment); i++ {
			c := segment[i]
			if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-._~!$&'()*+,;=:@", c) >= 0 {
				target.WriteByte(c)
			} else {
				fmt.Fprintf(&target, "%%%02X", c)
			}
		}
	}
	if trailing || len(segments) == 0 {
		target.WriteByte('/')
	}
	return target.String()
}
