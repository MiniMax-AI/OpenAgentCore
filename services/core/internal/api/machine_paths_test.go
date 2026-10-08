package api

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// Machine registration receives canonical paths before routing:
// no request is redirected, and a dirty or encoded path reaches exactly the
// handler its canonical path reaches, with the body intact (HP-17/HP-18).
func TestMachineHandlerRoutesCanonicalPaths(t *testing.T) {
	type observation struct{ route, method, path, rawPath, body string }
	var seen []observation
	sentinel := func(route string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			seen = append(seen, observation{route, r.Method, r.URL.Path, r.URL.RawPath, string(body)})
			w.WriteHeader(http.StatusNoContent)
		})
	}
	handler := machineSentinels(sentinel("api"), sentinel("gateway"), sentinel("enrollment"), sentinel("connection"), sentinel("node"))
	for _, test := range []struct{ target, route, path, rawPath string }{
		{"/v1/agents/a", "api", "/v1/agents/a", ""},
		{"/v1//agents/a", "api", "/v1/agents/a", ""},
		{"//v1/agents", "api", "/v1/agents", ""},
		{"/v1/agents/x/../a", "api", "/v1/agents/a", ""},
		{"/v1/agents/agent%5Fa", "api", "/v1/agents/agent_a", ""},
		{"/v1/agents/a%2Fb", "api", "/v1/agents/a/b", "/v1/agents/a%2Fb"},
		{"/api/v1/agent-daemon/%2E%2E/%2E%2E/%2E%2E/v1/agents", "api", "/v1/agents", ""},
		{"/api/v1/agent-daemon/../../../core/v1/sandbox/nodes", "api", "/core/v1/sandbox/nodes", ""},

		{"/api/v1/agent-daemon/ws", "gateway", "/api/v1/agent-daemon/ws", ""},
		{"/api/v1//agent-daemon/ws", "gateway", "/api/v1/agent-daemon/ws", ""},
		{"/v1/../api/v1/agent-daemon/enroll", "enrollment", "/api/v1/agent-daemon/enroll", ""},
		{"/v1/%2E%2E/api/v1/agent-daemon/connection", "connection", "/api/v1/agent-daemon/connection", ""},
		{"/api/v1/agent-daemon/%63onnection", "connection", "/api/v1/agent-daemon/connection", ""},
		{"/api/v1/sandbox-node//connect", "node", "/api/v1/sandbox-node/connect", ""},
		{"/api/v1/agent-daemon/%2E%2E/sandbox-node/connect", "node", "/api/v1/sandbox-node/connect", ""},
		{"/core/v1/sandbox/node/connect", "api", "/core/v1/sandbox/node/connect", ""},
	} {
		seen = nil
		request := httptest.NewRequest(http.MethodGet, test.target, strings.NewReader(`{"model":"x"}`))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		want := observation{test.route, http.MethodGet, test.path, test.rawPath, `{"model":"x"}`}
		if response.Code != http.StatusNoContent || response.Header().Get("Location") != "" || len(seen) != 1 || seen[0] != want {
			t.Errorf("%s = %d %v, want %v", test.target, response.Code, seen, want)
		}
	}
}

// daemonComposition exercises the API's machine registration with sentinel
// transport handlers. Other calls hit strict API dependencies.
func daemonComposition(t testing.TB) http.Handler {
	deps, _ := testDependencies(trapTB{t})
	sentinel := func(route string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Sentinel", route+" "+r.URL.Path+" "+r.URL.RawPath)
			w.WriteHeader(http.StatusNoContent)
		})
	}
	deps.Execution.Bootstrap, deps.Execution.RuntimeConnect = sentinel("gateway"), sentinel("gateway")
	deps.Execution.Enrollment, deps.Execution.Connection = sentinel("enrollment"), sentinel("connection")
	deps.Sandboxes.NodeConnect = sentinel("node")
	return newTestHandler(t, deps)
}

// machineSentinels uses the production registration, with no duplicate route
// table. Requests outside it are observed by the API sentinel.
func machineSentinels(apiHandler, gateway, enrollment, connection, node http.Handler) http.Handler {
	h := &Handler{Dependencies: Dependencies{Execution: Execution{Bootstrap: gateway, RuntimeConnect: gateway, Enrollment: enrollment, Connection: connection, Links: gateway}, Sandboxes: Sandboxes{NodeConnect: node}}}
	router := chi.NewRouter()
	router.NotFound(apiHandler.ServeHTTP)
	h.registerMachineRoutes(router)
	return CanonicalPaths(router)
}

func machineParseRaw(target string) (*http.Request, error) {
	return http.ReadRequest(bufio.NewReader(strings.NewReader("GET " + target + " HTTP/1.1\r\nHost: example.test\r\n\r\n")))
}

func machineOutcome(handler http.Handler, request *http.Request) (result string) {
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
	CanonicalPaths(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r.URL.EscapedPath() })).ServeHTTP(httptest.NewRecorder(), request)
	return seen
}

// In the machine route configuration, a raw path with bytes that are invalid
// in an escaped path cannot turn %2F into a separator to reach daemon, node or
// sandbox administration routes; it reaches what its canonical form reaches.
func TestMachineHandlerRawPathsKeepEncodedSeparators(t *testing.T) {
	handler := daemonComposition(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	for _, test := range []struct{ target, want string }{
		{"/v1/x{/..%2F..%2Fapi/v1/agent-daemon/enroll", "400"},
		{"/v1/x\"/..%2f..%2fapi/v1/agent-daemon/connection", "400"},
		{"/v1/\xc3\xa9/..%2F..%2Fapi/v1/sandbox-node/connect", "400"},
		{"/v1/x{/..%2F..%2Fcore/v1/sandbox/nodes", "400"},
		{"/v1/x{/..%2F..%2Fcore/v1/project-api-keys/x", "400"},
		{"/v1/x{/../../core/v1/api-keys/x", "401"},
		{"/v1/x\\/..%5C..%5Capi/v1/agent-daemon/ws", "400"},
		{"http://example.test/v1/x{/..%252F..%252Fapi/v1/agent-daemon/enroll", "400"},
		{"/v1/x{/../../api/v1/agent-daemon/enroll", "204"},
		{"/v1/x{/%2E%2E/%2E%2E/api/v1/sandbox-node/connect", "204"},
	} {
		request, err := machineParseRaw(test.target)
		if err != nil {
			t.Fatal(err)
		}
		again, _ := machineParseRaw(canonical(request))
		got, want := machineOutcome(handler, request), machineOutcome(handler, again)
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

// Differential property for the machine route configuration: any request path
// reaches the same handler, with the same path, as its canonical form.
func FuzzMachineHandlerRoutesLikeCanonicalForm(f *testing.F) {
	for _, seed := range []string{"v1//agents", "v1/x{/..%2F..%2Fapi/v1/agent-daemon/enroll", "api/v1/agent-daemon%2Fenroll",
		"v1/\xc3\xa9/../../api/v1/agent-daemon/ws", "api/v1/sandbox-node/%2E%2E/sandbox-node/connect", "0\"%2F", "api/v1/agent-daemon",
		"v1/x\\/..%5C..%5Capi/v1/agent-daemon/connection", "v1/agents/%252F%2e%2E/x", "v1/x{/..%2F..%2Fcore/v1/project-api-keys/x",
		"core/v1/projects/x/%2E%2E/%2E%2E/%2E%2E/%2E%2E/api/v1/agent-daemon/enroll"} {
		f.Add(seed)
	}
	handler := daemonComposition(f)
	// Every route of the sentinel composition reports the path it was served on,
	// as chi reads it: RawPath when set, otherwise the decoded Path.
	sentinel := func(route string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			routed := r.URL.RawPath
			if routed == "" {
				routed = r.URL.EscapedPath()
			}
			w.Header().Set("X-Route", route)
			w.Header().Set("X-Routed-Path", routed)
			w.WriteHeader(http.StatusNoContent)
		})
	}
	observed := machineSentinels(sentinel("api"), sentinel("gateway"), sentinel("enrollment"), sentinel("connection"), sentinel("node"))
	f.Fuzz(func(t *testing.T, path string) {
		if strings.ContainsAny(path, " ?#") || len(path) > 512 {
			t.Skip()
		}
		segments, trailing, ok := oraclePath("/" + path)
		request, err := machineParseRaw("/" + path)
		if err != nil || !ok {
			t.Skip()
		}
		again, err := machineParseRaw(canonical(request))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := machineOutcome(handler, request), machineOutcome(handler, again); got != want {
			t.Fatalf("%q = %s; canonical %q gives %s", path, got, again.RequestURI, want)
		}
		// Independently of CanonicalPaths: the router dispatches to the same
		// route as the oracle's spelling, and the handler sees the oracle's segments.
		oracle, err := machineParseRaw(oracleTarget(segments, trailing))
		if err != nil {
			t.Fatal(err)
		}
		request, _ = machineParseRaw("/" + path)
		served, expected := httptest.NewRecorder(), httptest.NewRecorder()
		observed.ServeHTTP(served, request)
		observed.ServeHTTP(expected, oracle)
		if served.Code != expected.Code || served.Header().Get("X-Route") != expected.Header().Get("X-Route") {
			t.Fatalf("%q = %d %s; oracle %q gives %d %s", path, served.Code, served.Header().Get("X-Route"), oracle.RequestURI, expected.Code, expected.Header().Get("X-Route"))
		}
		if served.Code == http.StatusNoContent {
			if got, gotTrailing := routedSegments(served.Header().Get("X-Routed-Path")); !slices.Equal(got, segments) || gotTrailing != trailing {
				t.Fatalf("%q is served on %q %v; the oracle gives %q %v", path, got, gotTrailing, segments, trailing)
			}
		}
	})
}
