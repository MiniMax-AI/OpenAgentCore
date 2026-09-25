package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testCoreKey = "deployment-core-key"

func coreKeyConsoleConfig(t *testing.T, backend http.Handler) config {
	t.Helper()
	upstream := httptest.NewServer(backend)
	t.Cleanup(upstream.Close)
	u, _ := url.Parse(upstream.URL)
	dist := t.TempDir()
	if err := os.Mkdir(filepath.Join(dist, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"index.html": "console application", "assets/main.js": "app script", "private.txt": "not public"} {
		if err := os.WriteFile(filepath.Join(dist, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return config{origin: testOrigin, upstream: u, dist: dist, coreKey: testCoreKey}
}

func startConsole(t *testing.T, c config) *console {
	t.Helper()
	h, err := newConsole(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

func authRequest(h *console, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = h.host
	r.Header.Set("Origin", h.origin)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func coreKeyInput(key string) string {
	body, _ := json.Marshal(map[string]string{"core_key": key})
	return string(body)
}

func signIn(t *testing.T, h *console) *http.Cookie {
	t.Helper()
	w := authRequest(h, "POST", "/console/auth/login", coreKeyInput(h.coreKey), nil)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"mode":"authenticated"}` {
		t.Fatalf("sign-in failed: %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("sign-in did not issue exactly one session cookie")
	}
	return cookies[0]
}

// Tests that talk to a real listener reuse one session per server, so the
// sign-in rate limit is not consumed by every request.
var testSessions sync.Map

func serveSignedIn(t *testing.T, h *console) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	testSessions.Store(server.URL, signIn(t, h))
	return server
}

func TestCoreKeySignInSessionAndLogout(t *testing.T) {
	var calls atomic.Int32
	c := coreKeyConsoleConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+testCoreKey || r.Header.Get("Cookie") != "" {
			t.Error("console failed to isolate upstream credentials")
		}
		w.WriteHeader(200)
	}))
	h := startConsole(t, c)
	if w := authRequest(h, "GET", "/console/auth", "", nil); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"mode":"login"}` {
		t.Fatalf("initial mode: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/core/v1/projects", "/console/config", "/core/v1/sandbox/nodes", "/private.txt"} {
		if w := authRequest(h, "GET", path, "", nil); w.Code != 401 {
			t.Errorf("private path %s returned %d", path, w.Code)
		}
	}
	for _, path := range []string{"/", "/index.html", "/assets/main.js"} {
		if w := authRequest(h, "GET", path, "", nil); w.Code != 200 {
			t.Errorf("login asset %s returned %d", path, w.Code)
		}
	}
	wrong := authRequest(h, "POST", "/console/auth/login", coreKeyInput("incorrect-core-key"), nil)
	if wrong.Code != 401 || len(wrong.Result().Cookies()) != 0 || !strings.Contains(wrong.Body.String(), `"error"`) {
		t.Fatalf("wrong key: %d %s", wrong.Code, wrong.Body)
	}
	if calls.Load() != 0 {
		t.Fatal("unauthenticated request reached Core")
	}
	cookie := signIn(t, h)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge != 43200 || cookie.Secure {
		t.Fatalf("unsafe cookie: %+v", cookie)
	}
	if w := authRequest(h, "GET", "/console/auth", "", cookie); strings.TrimSpace(w.Body.String()) != `{"mode":"authenticated"}` {
		t.Fatalf("signed-in mode: %s", w.Body)
	}
	for _, path := range []string{"/core/v1/projects", "/core/v1/sandbox/nodes", "/console/config"} {
		w := authRequest(h, "GET", path, "", cookie)
		if w.Code != 200 || strings.Contains(w.Body.String(), testCoreKey) {
			t.Errorf("authenticated path %s failed or leaked the Core key: %d", path, w.Code)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("authenticated management requests did not reach Core")
	}
	if w := authRequest(startConsole(t, c), "GET", "/console/auth", "", cookie); !strings.Contains(w.Body.String(), `"login"`) {
		t.Fatal("a restarted console accepted a previous session")
	}
	logout := authRequest(h, "POST", "/console/auth/logout", "", cookie)
	if logout.Code != 200 || strings.TrimSpace(logout.Body.String()) != `{"mode":"login"}` || logout.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout response did not clear the session")
	}
	if w := authRequest(h, "GET", "/core/v1/projects", "", cookie); w.Code != 401 {
		t.Fatal("logged-out cookie retained authority")
	}
}

func TestCoreKeySignInRejectsMalformedAndCrossOriginRequests(t *testing.T) {
	h := startConsole(t, coreKeyConsoleConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected proxy call") })))
	for _, path := range []string{"/console/auth", "/console/auth/login", "/console/auth/logout"} {
		method := "POST"
		if path == "/console/auth" {
			method = "GET"
		}
		for _, modify := range []func(*http.Request){
			func(r *http.Request) { r.Host = "attacker.example" },
			func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") },
			func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
		} {
			r := httptest.NewRequest(method, path, strings.NewReader(coreKeyInput(testCoreKey)))
			r.Host = h.host
			r.Header.Set("Origin", testOrigin)
			modify(r)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 403 || !strings.Contains(w.Body.String(), `"error"`) {
				t.Errorf("cross-origin %s returned %d", path, w.Code)
			}
		}
	}
	for _, body := range []string{"", "null", `[]`, `{}`, `{"core_key":""}`, `{"core_key":null}`, `{"core_key":1}`,
		`{"CORE_KEY":"` + testCoreKey + `"}`, `{"core_key":"` + testCoreKey + `","username":"admin"}`,
		coreKeyInput(testCoreKey) + `{}`, coreKeyInput(strings.Repeat("x", 4100))} {
		if w := authRequest(h, "POST", "/console/auth/login", body, nil); w.Code != 400 || strings.Contains(w.Body.String(), testCoreKey) {
			t.Errorf("malformed body %.40q returned %d", body, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/console/auth/login", strings.NewReader(coreKeyInput(testCoreKey)))
	r.Host = h.host
	r.Header.Set("Origin", testOrigin)
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatalf("non-JSON sign-in returned %d", w.Code)
	}
	for _, path := range []string{"/console/auth/login", "/console/auth/logout"} {
		if w := authRequest(h, "GET", path, "", nil); w.Code != 405 {
			t.Errorf("GET mutation %s returned %d", path, w.Code)
		}
	}
	if w := authRequest(h, "POST", "/console/auth/setup", `{"username":"owner","password":"retired-password"}`, nil); w.Code != 404 {
		t.Fatalf("retired setup route returned %d", w.Code)
	}
	r = httptest.NewRequest("GET", "/core/v1/projects", nil)
	r.Host = h.host
	r.SetBasicAuth("admin", testCoreKey)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("Basic authentication is still accepted or advertised")
	}
	r = httptest.NewRequest("POST", "/console/auth/login", strings.NewReader(coreKeyInput(testCoreKey)))
	r.Host = h.host
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("sign-in without browser origin evidence was accepted")
	}
}

func TestFailedSignInsNeverLockOutTheCoreKey(t *testing.T) {
	h := startConsole(t, coreKeyConsoleConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
	for attempt := 0; attempt < 10; attempt++ {
		if w := authRequest(h, "POST", "/console/auth/login", coreKeyInput("incorrect"), nil); w.Code != 401 {
			t.Fatalf("attempt %d unexpectedly returned %d", attempt, w.Code)
		}
	}
	limited := authRequest(h, "POST", "/console/auth/login", coreKeyInput("incorrect"), nil)
	if limited.Code != 429 || limited.Header().Get("Retry-After") != "60" || len(limited.Result().Cookies()) != 0 {
		t.Fatal("failed sign-ins were not limited")
	}
	signIn(t, h)
}

func TestSecureCookieExpiry(t *testing.T) {
	c := coreKeyConsoleConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("expired session reached Core") }))
	c.origin = "https://127.0.0.1:8080"
	h := startConsole(t, c)
	cookie := signIn(t, h)
	if !cookie.Secure {
		t.Fatal("HTTPS session cookie omitted Secure")
	}
	h.auth.mu.Lock()
	for key := range h.auth.sessions {
		h.auth.sessions[key] = time.Now().Add(-time.Second)
	}
	h.auth.mu.Unlock()
	if w := authRequest(h, "GET", "/core/v1/projects", "", cookie); w.Code != 401 {
		t.Fatal("expired session retained authority")
	}
}
