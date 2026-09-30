package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testOrigin = "http://127.0.0.1:8080"

func testConsole(t *testing.T, backend http.Handler) (*httptest.Server, string) {
	t.Helper()
	upstream := httptest.NewServer(backend)
	t.Cleanup(upstream.Close)
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<html>existing web build</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler, err := newConsole(config{origin: testOrigin, upstream: u, dist: dist, coreKey: "private-core-key"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(handler.Close)
	return serveSignedIn(t, handler), dist
}

func consoleRequest(t *testing.T, server *httptest.Server, method, path string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(method, server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "127.0.0.1:8080"
	if cookie, ok := testSessions.Load(server.URL); ok {
		r.AddCookie(cookie.(*http.Cookie))
	}
	r.Header.Set("Origin", testOrigin)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	return r
}

func responseBody(t *testing.T, server *httptest.Server, r *http.Request) (*http.Response, string) {
	t.Helper()
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, string(body)
}

func TestAuthenticationAndCrossSiteAdmission(t *testing.T) {
	var calls atomic.Int32
	server, _ := testConsole(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "[]")
	}))
	cases := []struct {
		name   string
		method string
		change func(*http.Request)
		status int
	}{
		{"missing session", "GET", func(r *http.Request) { r.Header.Del("Cookie") }, 401},
		{"unknown session", "GET", func(r *http.Request) { r.Header.Set("Cookie", sessionCookie+"="+strings.Repeat("0", 64)) }, 401},
		{"duplicate session", "GET", func(r *http.Request) { r.Header.Add("Cookie", r.Header.Get("Cookie")) }, 401},
		{"Core key as bearer", "GET", func(r *http.Request) {
			r.Header.Del("Cookie")
			r.Header.Set("Authorization", "Bearer private-core-key")
		}, 401},
		{"DNS rebinding host", "GET", func(r *http.Request) { r.Host = "attacker.example:8080" }, 403},
		{"cross origin", "POST", func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") }, 403},
		{"null origin", "POST", func(r *http.Request) { r.Header.Set("Origin", "null") }, 403},
		{"duplicate origin", "POST", func(r *http.Request) { r.Header.Add("Origin", testOrigin) }, 403},
		{"cross-site GET", "GET", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
		{"same-site subdomain", "POST", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }, 403},
		{"missing mutation provenance", "POST", func(r *http.Request) {
			r.Header.Del("Origin")
			r.Header.Del("Sec-Fetch-Site")
		}, 403},
		{"upgrade", "GET", func(r *http.Request) { r.Header.Set("Upgrade", "websocket") }, 400},
		{"TRACE credential reflection", "TRACE", func(_ *http.Request) {}, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := consoleRequest(t, server, tc.method, "/core/v1/projects")
			tc.change(r)
			response, body := responseBody(t, server, r)
			if response.StatusCode != tc.status {
				t.Fatalf("status = %d, body = %s", response.StatusCode, body)
			}
			code := map[int]string{401: "console_sign_in_required", 403: "console_origin_rejected", 400: "console_request_invalid"}[tc.status]
			assertConsoleCoreError(t, body, code, tc.status)
			if strings.Contains(body, "private-") {
				t.Fatal("rejection exposed a credential")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("rejected request reached Core")
	}
	anonymous := consoleRequest(t, server, "GET", "/sessions/saved")
	anonymous.Header.Del("Cookie")
	response, body := responseBody(t, server, anonymous)
	if response.StatusCode != 401 || strings.Contains(body, "existing web build") {
		t.Fatal("application routes bypassed console authentication")
	}
	request := consoleRequest(t, server, "GET", "/healthz")
	request.Header = make(http.Header)
	request.Host = "healthcheck"
	response, body = responseBody(t, server, request)
	if response.StatusCode != 200 || body != "ok\n" {
		t.Fatal("liveness requires authentication")
	}
}

func TestProxyUsesOnlyConfiguredCoreCredential(t *testing.T) {
	observed := make(chan *http.Request, 1)
	server, _ := testConsole(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- r.Clone(context.Background())
		w.Header().Set("Set-Cookie", "upstream=credential")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	r := consoleRequest(t, server, "POST", "/core/v1/projects?limit=5")
	r.Header.Add("Cookie", "browser=private")
	r.Header.Set("Authorization", "Bearer browser-token")
	r.Header.Set("Proxy-Authorization", "Basic browser-secret")
	r.Header.Set("OpenAI-Beta", "agents=v1")
	r.Header.Set("Forwarded", "host=attacker.example")
	r.Header.Set("X-Forwarded-Host", "attacker.example")
	response, body := responseBody(t, server, r)
	if response.StatusCode != 200 || body != `{"data":[]}` {
		t.Fatalf("proxy response: %d %s", response.StatusCode, body)
	}
	for _, name := range []string{"Set-Cookie", "Access-Control-Allow-Origin", "WWW-Authenticate"} {
		if response.Header.Get(name) != "" {
			t.Errorf("upstream %s reached browser", name)
		}
	}
	request := <-observed
	if request.URL.RequestURI() != "/core/v1/projects?limit=5" || request.Header.Get("Authorization") != "Bearer private-core-key" || request.Header.Get("OpenAI-Beta") != "agents=v1" {
		t.Fatal("proxy changed the public request or failed to inject the Core key")
	}
	for _, name := range []string{"Cookie", "Proxy-Authorization", "Origin", "Referer", "Forwarded", "X-Forwarded-Host"} {
		if request.Header.Get(name) != "" {
			t.Errorf("browser %s reached Core", name)
		}
	}
}

func TestProxyRejectsRedirectWithoutFollowingOrExposingIt(t *testing.T) {
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinationCalls.Add(1)
		w.WriteHeader(200)
	}))
	defer destination.Close()
	server, _ := testConsole(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/?token=private-core-key", http.StatusTemporaryRedirect)
	}))
	response, body := responseBody(t, server, consoleRequest(t, server, "GET", "/core/v1/projects"))
	assertConsoleCoreError(t, body, "core_unreachable", 502)
	if response.StatusCode != 502 || response.Header.Get("Location") != "" || strings.Contains(body, "private-core-key") || destinationCalls.Load() != 0 {
		t.Fatal("upstream redirect escaped the fixed proxy")
	}
}

func TestArtifactProxyFlushesContentAndCancelsUpstream(t *testing.T) {
	cancelled := make(chan struct{})
	server, _ := testConsole(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = fmt.Fprint(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := consoleRequest(t, server, "GET", "/core/v1/projects/key/sessions/session/artifacts/artifact/content").WithContext(ctx)
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("artifact content did not flush: %q %v", line, err)
	}
	response.Body.Close()
	cancel()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not cancel Core request")
	}
}

func TestStaticAssetsStayInsideDistAndInternalRoutesStayLocal(t *testing.T) {
	var calls atomic.Int32
	server, dist := testConsole(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}))
	outside := filepath.Join(t.TempDir(), "caller.key")
	if err := os.WriteFile(outside, []byte("outside-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dist, "leak.key")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "main.js"), []byte("console.log('existing');"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/", 200}, {"/sessions/saved", 200}, {"/assets/main.js", 200},
		{"/assets/", 404}, {"/missing.js", 404}, {"/leak.key", 404},
		{"/../caller.key", 400}, {"/%2e%2e/caller.key", 400}, {"/%252e%252e/caller.key", 400},
		{"/v1/../api/v1/agent-daemon/ws", 404}, {"/v1//agents", 404},
		{"/api/v1/agent-daemon/ws", 404},
	} {
		t.Run(tc.path, func(t *testing.T) {
			response, body := responseBody(t, server, consoleRequest(t, server, "GET", tc.path))
			if response.StatusCode != tc.status || strings.Contains(body, "outside-secret") {
				t.Fatalf("static response = %d %s", response.StatusCode, body)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("non-API path reached Core")
	}
}
