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

const testAccountPassword = "correct-password-for-console"

func accountConsoleConfig(t *testing.T, backend http.Handler) config {
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
	state := t.TempDir()
	if err := os.Chmod(state, 0o700); err != nil {
		t.Fatal(err)
	}
	return config{origin: testOrigin, upstream: u, dist: dist, adminToken: "deployment-token",
		authMode: "account", stateDir: state}
}

func accountConsole(t *testing.T, c config) *console {
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

func accountInput(username, password string) string {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	return string(body)
}

func setupAccount(t *testing.T, h *console) *http.Cookie {
	t.Helper()
	w := authRequest(h, "POST", "/console/auth/setup", accountInput("owner", testAccountPassword), nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"mode":"authenticated"`) {
		t.Fatalf("setup failed: %d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("setup did not issue exactly one session cookie")
	}
	return cookies[0]
}

func TestAccountSetupLoginRestartAndLogout(t *testing.T) {
	var calls atomic.Int32
	c := accountConsoleConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		want := "Bearer deployment-token"
		if strings.HasPrefix(r.URL.Path, "/core/") {
			want = "Bearer deployment-token"
		}
		if r.Header.Get("Authorization") != want || r.Header.Get("Cookie") != "" {
			t.Error("console failed to isolate upstream credentials")
		}
		w.WriteHeader(200)
	}))
	h := accountConsole(t, c)
	if w := authRequest(h, "GET", "/console/auth", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"setup"`) {
		t.Fatalf("initial mode: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/core/v1/admin/projects", "/console/config", "/core/v1/sandbox/nodes", "/private.txt", "/state/console/admin.json"} {
		if w := authRequest(h, "GET", path, "", nil); w.Code != 401 {
			t.Errorf("private path %s returned %d", path, w.Code)
		}
	}
	for _, path := range []string{"/", "/index.html", "/assets/main.js"} {
		if w := authRequest(h, "GET", path, "", nil); w.Code != 200 {
			t.Errorf("login asset %s returned %d", path, w.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unauthenticated request reached Core")
	}
	cookie := setupAccount(t, h)
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge != 43200 || cookie.Secure {
		t.Fatalf("unsafe cookie: %+v", cookie)
	}
	for _, path := range []string{"/core/v1/admin/projects", "/core/v1/sandbox/nodes", "/console/config"} {
		w := authRequest(h, "GET", path, "", cookie)
		if w.Code != 200 || strings.Contains(w.Body.String(), c.adminToken) || strings.Contains(w.Body.String(), c.adminToken) {
			t.Errorf("authenticated path %s failed or leaked a credential: %d", path, w.Code)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("authenticated public/admin requests did not reach Core")
	}
	if w := authRequest(h, "POST", "/console/auth/setup", accountInput("other", testAccountPassword), nil); w.Code != 409 {
		t.Fatal("registration remained open after setup")
	}
	stored, err := os.ReadFile(filepath.Join(c.stateDir, accountFilename))
	if err != nil || strings.Contains(string(stored), testAccountPassword) {
		t.Fatal("account was not persisted safely")
	}
	info, _ := os.Stat(filepath.Join(c.stateDir, accountFilename))
	if info.Mode().Perm() != 0o600 {
		t.Fatal("account file is not private")
	}
	restarted := accountConsole(t, c)
	if w := authRequest(restarted, "GET", "/console/auth", "", cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"login"`) {
		t.Fatal("restart lost the account or retained the old session")
	}
	if w := authRequest(restarted, "POST", "/console/auth/login", accountInput("owner", "incorrect-password"), nil); w.Code != 401 {
		t.Fatal("incorrect password accepted")
	}
	loggedIn := authRequest(restarted, "POST", "/console/auth/login", accountInput("owner", testAccountPassword), nil)
	if loggedIn.Code != 200 {
		t.Fatalf("persistent account login failed: %d", loggedIn.Code)
	}
	newCookie := loggedIn.Result().Cookies()[0]
	logout := authRequest(restarted, "POST", "/console/auth/logout", "", newCookie)
	if logout.Code != 200 || !strings.Contains(logout.Body.String(), `"login"`) || logout.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("logout response did not clear the session")
	}
	if w := authRequest(restarted, "GET", "/core/v1/admin/projects", "", newCookie); w.Code != 401 {
		t.Fatal("logged-out cookie retained authority")
	}
}

func TestAccountOriginChecksAndRequestBounds(t *testing.T) {
	c := accountConsoleConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected proxy call") }))
	h := accountConsole(t, c)
	for _, path := range []string{"/console/auth", "/console/auth/setup", "/console/auth/login", "/console/auth/logout"} {
		method := "POST"
		if path == "/console/auth" {
			method = "GET"
		}
		for _, modify := range []func(*http.Request){
			func(r *http.Request) { r.Host = "attacker.example" },
			func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") },
			func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
		} {
			r := httptest.NewRequest(method, path, strings.NewReader(accountInput("owner", testAccountPassword)))
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
	for _, body := range []string{
		accountInput("invalid user", testAccountPassword),
		accountInput("owner", "short"),
		accountInput("owner", strings.Repeat("界", 25)),
		accountInput("owner", strings.Repeat("x", 4100)),
		`{"username":"owner","password":"valid-password","unexpected":true}`,
		accountInput("owner", testAccountPassword) + `{}`,
	} {
		if w := authRequest(h, "POST", "/console/auth/setup", body, nil); w.Code != 400 {
			t.Errorf("invalid body returned %d", w.Code)
		}
	}
	for _, path := range []string{"/console/auth/setup", "/console/auth/login", "/console/auth/logout"} {
		if w := authRequest(h, "GET", path, "", nil); w.Code != 405 {
			t.Errorf("GET mutation %s returned %d", path, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/console/auth/setup", strings.NewReader(accountInput("owner", testAccountPassword)))
	r.Host = h.host
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("mutation without browser origin evidence was accepted")
	}
	for attempt := 0; attempt < 10; attempt++ {
		w := authRequest(h, "POST", "/console/auth/login", accountInput("owner", testAccountPassword), nil)
		if w.Code != 409 {
			t.Fatalf("attempt %d unexpectedly returned %d", attempt, w.Code)
		}
	}
	if w := authRequest(h, "POST", "/console/auth/setup", accountInput("owner", testAccountPassword), nil); w.Code != 429 {
		t.Fatal("authentication rate limit was not enforced")
	}
}

func TestConcurrentAccountSetupAcrossInstances(t *testing.T) {
	c := accountConsoleConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	servers := []*console{accountConsole(t, c), accountConsole(t, c)}
	var group sync.WaitGroup
	results := make(chan int, 2)
	for index, server := range servers {
		group.Add(1)
		go func() {
			defer group.Done()
			username := []string{"alice", "bob"}[index]
			results <- authRequest(server, "POST", "/console/auth/setup", accountInput(username, testAccountPassword), nil).Code
		}()
	}
	group.Wait()
	close(results)
	counts := map[int]int{}
	for code := range results {
		counts[code]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatalf("registration race did not yield one winner: %v", counts)
	}
	for _, h := range servers {
		if w := authRequest(h, "GET", "/console/auth", "", nil); !strings.Contains(w.Body.String(), `"login"`) {
			t.Fatal("another instance did not observe registration")
		}
	}
	entries, _ := os.ReadDir(c.stateDir)
	if len(entries) != 2 || entries[0].Name() != accountFilename || entries[1].Name() != registeredFilename {
		t.Fatal("registration left incomplete or competing account files")
	}
}

func TestAccountSecureCookieExpiry(t *testing.T) {
	c := accountConsoleConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("expired session reached Core") }))
	c.origin = "https://127.0.0.1:8080"
	h := accountConsole(t, c)
	cookie := setupAccount(t, h)
	if !cookie.Secure {
		t.Fatal("HTTPS session cookie omitted Secure")
	}
	h.auth.mu.Lock()
	for key, session := range h.auth.sessions {
		session.expires = time.Now().Add(-time.Second)
		h.auth.sessions[key] = session
	}
	h.auth.mu.Unlock()
	if w := authRequest(h, "GET", "/core/v1/admin/projects", "", cookie); w.Code != 401 {
		t.Fatal("expired session retained authority")
	}
}
