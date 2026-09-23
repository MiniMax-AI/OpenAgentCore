package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

// The daemon-enabled configuration canonicalizes paths before the ServeMux:
// no request is redirected, and a dirty or encoded path reaches exactly the
// handler its canonical path reaches, with the body intact (HP-17/HP-18).
func TestServerHandlerRoutesCanonicalPaths(t *testing.T) {
	type observation struct{ route, method, path, rawPath, body string }
	var seen []observation
	sentinel := func(route string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			seen = append(seen, observation{route, r.Method, r.URL.Path, r.URL.RawPath, string(body)})
			w.WriteHeader(http.StatusNoContent)
		})
	}
	handler := serverHandler(sentinel("api"), &daemonRoutes{gateway: sentinel("gateway"), enrollment: sentinel("enrollment"),
		connection: sentinel("connection"), nodeConnect: sentinel("node")})
	for _, test := range []struct{ target, route, path, rawPath string }{
		{"/v1/agents/a", "api", "/v1/agents/a", ""},
		{"/v1//agents/a", "api", "/v1/agents/a", ""},
		{"//v1/agents", "api", "/v1/agents", ""},
		{"/v1/agents/x/../a", "api", "/v1/agents/a", ""},
		{"/v1/agents/agent%5Fa", "api", "/v1/agents/agent_a", ""},
		{"/v1/agents/a%2Fb", "api", "/v1/agents/a/b", "/v1/agents/a%2Fb"},
		{"/api/v1/agent-daemon/%2E%2E/%2E%2E/%2E%2E/v1/agents", "api", "/v1/agents", ""},
		{"/api/v1/agent-daemon/../../../core/v1/sandbox/nodes", "api", "/core/v1/sandbox/nodes", ""},
		{"/api/v1/agent-daemon%2Fenroll", "api", "/api/v1/agent-daemon/enroll", "/api/v1/agent-daemon%2Fenroll"},
		{"/api/v1/agent-daemon/ws", "gateway", "/api/v1/agent-daemon/ws", ""},
		{"/api/v1//agent-daemon/ws", "gateway", "/api/v1/agent-daemon/ws", ""},
		{"/v1/../api/v1/agent-daemon/enroll", "enrollment", "/api/v1/agent-daemon/enroll", ""},
		{"/v1/%2E%2E/api/v1/agent-daemon/connection", "connection", "/api/v1/agent-daemon/connection", ""},
		{"/api/v1/agent-daemon/%63onnection", "connection", "/api/v1/agent-daemon/connection", ""},
		{"/core/v1/sandbox/node//connect", "node", "/core/v1/sandbox/node/connect", ""},
		{"/core/v1/sandbox/nodes/%2E%2E/node/connect", "node", "/core/v1/sandbox/node/connect", ""},
	} {
		seen = nil
		request := httptest.NewRequest(http.MethodPost, test.target, strings.NewReader(`{"model":"x"}`))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		want := observation{test.route, http.MethodPost, test.path, test.rawPath, `{"model":"x"}`}
		if response.Code != http.StatusNoContent || response.Header().Get("Location") != "" || len(seen) != 1 || seen[0] != want {
			t.Errorf("%s = %d %v, want %v", test.target, response.Code, seen, want)
		}
	}
}

// trapStore panics on every store call, marking a request that reached a handler.
type trapStore struct{ api.ResourceStore }

// daemonComposition serves the real API handler beside sentinel daemon routes.
func daemonComposition(t testing.TB) http.Handler {
	t.Helper()
	auth, err := api.NewAuthenticator([]api.APIKey{{OrganizationID: "org", ProjectID: "project", SubjectKind: "service_account",
		SubjectID: "runner", TokenSHA256: device.HashCredential("project-key"), TenantID: uuid.NewString()}})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := api.NewDeploymentAuthenticator([]string{device.HashCredential("admin-key")})
	if err != nil {
		t.Fatal(err)
	}
	apiHandler, err := api.NewHandler(trapStore{}, auth, "codex", api.WithSandboxManager(&store.Store{}, admin))
	if err != nil {
		t.Fatal(err)
	}
	sentinel := func(route string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Sentinel", route+" "+r.URL.Path+" "+r.URL.RawPath)
			w.WriteHeader(http.StatusNoContent)
		})
	}
	return serverHandler(apiHandler, &daemonRoutes{gateway: sentinel("gateway"), enrollment: sentinel("enrollment"),
		connection: sentinel("connection"), nodeConnect: sentinel("node")})
}

func parseRaw(target string) (*http.Request, error) {
	return http.ReadRequest(bufio.NewReader(strings.NewReader("GET " + target + " HTTP/1.1\r\nHost: example.test\r\n\r\n")))
}

func outcome(handler http.Handler, request *http.Request) (result string) {
	defer func() {
		if recover() != nil {
			result = "handler reached"
		}
	}()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return fmt.Sprintf("%d %q %q %q %q", response.Code, response.Header().Get("X-Sentinel"), response.Body.String(), response.Header().Get("Allow"), response.Header().Get("Location"))
}

// canonical returns the escaped path the composition routes a request on.
func canonical(request *http.Request) string {
	var seen string
	api.CanonicalPaths(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r.URL.EscapedPath() })).ServeHTTP(httptest.NewRecorder(), request)
	return seen
}

// In the daemon-enabled configuration, a raw path with bytes that are invalid
// in an escaped path cannot turn %2F into a separator to reach daemon, node or
// sandbox administration routes; it reaches what its canonical form reaches.
func TestServerHandlerRawPathsKeepEncodedSeparators(t *testing.T) {
	handler := daemonComposition(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	for _, test := range []struct{ target, want string }{
		{"/v1/x{/..%2F..%2Fapi/v1/agent-daemon/enroll", "400"},
		{"/v1/x\"/..%2f..%2fapi/v1/agent-daemon/connection", "400"},
		{"/v1/\xc3\xa9/..%2F..%2Fcore/v1/sandbox/node/connect", "400"},
		{"/v1/x{/..%2F..%2Fcore/v1/sandbox/nodes", "400"},
		{"/v1/x\\/..%5C..%5Capi/v1/agent-daemon/ws", "400"},
		{"http://example.test/v1/x{/..%252F..%252Fapi/v1/agent-daemon/enroll", "400"},
		{"/v1/x{/../../api/v1/agent-daemon/enroll", "204"},
		{"/v1/x{/%2E%2E/%2E%2E/core/v1/sandbox/node/connect", "204"},
	} {
		request, err := parseRaw(test.target)
		if err != nil {
			t.Fatal(err)
		}
		again, _ := parseRaw(canonical(request))
		got, want := outcome(handler, request), outcome(handler, again)
		if got != want || !strings.HasPrefix(got, test.want+" ") {
			t.Errorf("%s = %s; canonical form gives %s", test.target, got, want)
		}
		connection, err := net.Dial("tcp", server.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(connection, "GET "+test.target+" HTTP/1.1\r\nHost: example.test\r\nConnection: close\r\n\r\n")
		response, err := http.ReadResponse(bufio.NewReader(connection), nil)
		if err != nil || strconv.Itoa(response.StatusCode) != test.want {
			t.Errorf("raw %s = %v %v", test.target, response, err)
		}
		_ = connection.Close()
	}
}

// Differential property for the daemon-enabled configuration: any request path
// reaches the same handler, with the same path, as its canonical form.
func FuzzServerHandlerRoutesLikeCanonicalForm(f *testing.F) {
	for _, seed := range []string{"v1//agents", "v1/x{/..%2F..%2Fapi/v1/agent-daemon/enroll", "api/v1/agent-daemon%2Fenroll",
		"v1/\xc3\xa9/../../api/v1/agent-daemon/ws", "core/v1/sandbox/node/%2E%2E/node/connect", "0\"%2F", "api/v1/agent-daemon",
		"v1/x\\/..%5C..%5Capi/v1/agent-daemon/connection", "v1/agents/%252F%2e%2E/x"} {
		f.Add(seed)
	}
	handler := daemonComposition(f)
	f.Fuzz(func(t *testing.T, path string) {
		if strings.ContainsAny(path, " ?#") || len(path) > 512 {
			t.Skip()
		}
		request, err := parseRaw("/" + path)
		if err != nil {
			t.Skip()
		}
		again, err := parseRaw(canonical(request))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := outcome(handler, request), outcome(handler, again); got != want {
			t.Fatalf("%q = %s; canonical %q gives %s", path, got, again.RequestURI, want)
		}
	})
}
