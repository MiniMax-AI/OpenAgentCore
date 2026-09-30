package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
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
		{"/api/v1/sandbox-node//connect", "node", "/api/v1/sandbox-node/connect", ""},
		{"/api/v1/agent-daemon/%2E%2E/sandbox-node/connect", "node", "/api/v1/sandbox-node/connect", ""},
		{"/core/v1/sandbox/node/connect", "api", "/core/v1/sandbox/node/connect", ""},
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

// trapProjectsReader resolves the fixture Project keys; every other call panics.
type trapProjectsReader struct {
	api.ProjectsReader
	keys fixtureKeyResolver
}

func (p trapProjectsReader) ResolveAPIKey(ctx context.Context, digest [sha256.Size]byte) (projects.KeyBinding, error) {
	return p.keys.ResolveAPIKey(ctx, digest)
}

// daemonComposition serves the real API handler beside sentinel daemon routes.
// Every dependency call panics, marking a request that reached a handler.
func daemonComposition(t testing.TB) http.Handler {
	t.Helper()
	keys := newTestAuthenticator(t, []testAPIKey{{OrganizationID: "org", ProjectID: "project", SubjectKind: "service_account",
		SubjectID: "runner", TokenSHA256: runtimedevice.HashCredential("project-key"), TenantID: uuid.NewString()}})
	admin, err := api.NewDeploymentAuthenticator([]string{runtimedevice.HashCredential("admin-key")})
	if err != nil {
		t.Fatal(err)
	}
	apiHandler, err := api.NewHandler(api.Dependencies{
		Engine: "codex", CoreKeys: admin, InstallationBindings: struct{ api.InstallationBindings }{},
		Projects: struct{ api.Projects }{}, ProjectsReader: trapProjectsReader{keys: keys},
		ModelProviders: struct{ api.ModelProviders }{}, ModelProvidersReader: struct{ api.ModelProvidersReader }{},
		Vaults: struct{ api.Vaults }{}, VaultsReader: struct{ api.VaultsReader }{},
		Files: struct{ api.Files }{}, FilesReader: struct{ api.FilesReader }{},
		Skills: struct{ api.Skills }{}, SkillsReader: struct{ api.SkillsReader }{},
		Agents: struct{ api.Agents }{}, AgentsReader: struct{ api.AgentsReader }{}, Sessions: struct{ api.Sessions }{}, SessionEvents: struct{ api.SessionEvents }{},
		EnvironmentTemplates: struct{ api.EnvironmentTemplates }{}, EnvironmentTemplatesReader: struct{ api.EnvironmentTemplatesReader }{},
		SessionHistory: struct{ api.SessionHistory }{}, Subagents: struct{ api.Subagents }{}, Artifacts: struct{ api.Artifacts }{},
		SessionAdmin: struct{ api.SessionAdmin }{}, Environments: struct{ api.Environments }{}, ExecutorConnections: struct{ api.ExecutorConnections }{},
		Admin: struct{ api.Admin }{}, AdminAudit: struct{ api.AdminAudit }{}, WriteAudit: struct{ api.WriteAudit }{}, Metrics: struct{ api.Metrics }{},
		RuntimeObservations: struct{ api.RuntimeObservations }{}, RuntimeHistory: struct{ api.RuntimeHistory }{},
		Execution: &api.Execution{ExecutorURL: "wss://core.example/api/v1/agent-daemon/ws", Admission: struct{ api.Admission }{},
			SessionArchive: struct{ api.SessionArchive }{}, Workspaces: struct{ api.EnvironmentWorkspaces }{}},
		Sandboxes: &api.Sandboxes{Deployment: struct{ api.Deployment }{}, DeploymentChanges: struct{ api.DeploymentChanges }{},
			ConfigurationDiscovery: struct{ api.ConfigurationDiscovery }{}},
	})
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
		{"/v1/\xc3\xa9/..%2F..%2Fapi/v1/sandbox-node/connect", "400"},
		{"/v1/x{/..%2F..%2Fcore/v1/sandbox/nodes", "400"},
		{"/v1/x{/..%2F..%2Fcore/v1/project-api-keys/x", "400"},
		{"/v1/x{/../../core/v1/api-keys/x", "401"},
		{"/v1/x\\/..%5C..%5Capi/v1/agent-daemon/ws", "400"},
		{"http://example.test/v1/x{/..%252F..%252Fapi/v1/agent-daemon/enroll", "400"},
		{"/v1/x{/../../api/v1/agent-daemon/enroll", "204"},
		{"/v1/x{/%2E%2E/%2E%2E/api/v1/sandbox-node/connect", "204"},
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
	observed := serverHandler(sentinel("api"), &daemonRoutes{gateway: sentinel("gateway"), enrollment: sentinel("enrollment"),
		connection: sentinel("connection"), nodeConnect: sentinel("node")})
	f.Fuzz(func(t *testing.T, path string) {
		if strings.ContainsAny(path, " ?#") || len(path) > 512 {
			t.Skip()
		}
		segments, trailing, ok := oraclePath("/" + path)
		request, err := parseRaw("/" + path)
		if err != nil || !ok {
			t.Skip()
		}
		again, err := parseRaw(canonical(request))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := outcome(handler, request), outcome(handler, again); got != want {
			t.Fatalf("%q = %s; canonical %q gives %s", path, got, again.RequestURI, want)
		}
		// Independently of CanonicalPaths: the ServeMux dispatches to the same
		// route as the oracle's spelling, and the handler sees the oracle's segments.
		oracle, err := parseRaw(oracleTarget(segments, trailing))
		if err != nil {
			t.Fatal(err)
		}
		request, _ = parseRaw("/" + path)
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
