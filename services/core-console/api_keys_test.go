package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestConsoleAPIKeyBridgeRequiresLoginAndFixedServerBinding(t *testing.T) {
	var calls atomic.Int32
	parent := sha256.Sum256([]byte("project-token"))
	base := coreAPIKeysPath + hex.EncodeToString(parent[:])
	id := uuid.NewString()
	c := accountConsoleConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != base && r.URL.Path != base+"/"+id || r.URL.RawQuery != "" {
			t.Errorf("bridge accepted caller-selected binding/path: %s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer deployment-token" || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" {
			t.Error("key bridge did not isolate deployment authority")
		}
		if r.Method == "POST" {
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"id":"`+id+`","key":"new-independent-project-key"}`)
		} else {
			_, _ = io.WriteString(w, `{"data":[],"has_more":false}`)
		}
	}))
	h := accountConsole(t, c)
	for _, method := range []string{"GET", "POST", "DELETE"} {
		path := consoleAPIKeysPath
		if method == "DELETE" {
			path += "/" + id
		}
		if w := authRequest(h, method, path, `{}`, nil); w.Code != 401 {
			t.Errorf("unauthenticated key operation returned %d", w.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unauthenticated management reached Core")
	}
	cookie := setupAccount(t, h)
	for _, method := range []string{"GET", "POST", "DELETE"} {
		path := consoleAPIKeysPath
		status := 200
		if method == "DELETE" {
			path += "/" + id
		} else if method == "POST" {
			status = 201
		}
		r := httptest.NewRequest(method, path, strings.NewReader(`{"id":"`+id+`","name":"key"}`))
		r.Host = h.host
		r.Header.Set("Origin", h.origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer caller-cannot-select-binding")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status || strings.Contains(w.Body.String(), c.token) || strings.Contains(w.Body.String(), c.adminToken) || strings.Contains(w.Body.String(), hex.EncodeToString(parent[:])) {
			t.Errorf("key operation failed or leaked deployment credentials: %d %s", w.Code, w.Body)
		}
	}
	if calls.Load() != 3 {
		t.Fatal("bridge did not perform exactly the three requested operations")
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", consoleAPIKeysPath + "?binding_digest=other", 400},
		{"GET", consoleAPIKeysPath + "?", 400},
		{"GET", consoleAPIKeysPath + "/" + id, 405},
		{"DELETE", consoleAPIKeysPath + "/other/" + id, 404},
		{"DELETE", consoleAPIKeysPath + "/invalid", 404},
		{"PATCH", consoleAPIKeysPath, 405},
		{"GET", base, 404},
	} {
		if w := authRequest(h, tc.method, tc.path, `{}`, cookie); w.Code != tc.status {
			t.Errorf("%s %s returned %d, want %d", tc.method, tc.path, w.Code, tc.status)
		}
	}
	r := httptest.NewRequest("POST", consoleAPIKeysPath, strings.NewReader(`{}`))
	r.Host = h.host
	r.Header.Set("Origin", "https://attacker.example")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || calls.Load() != 3 {
		t.Fatal("unsafe bridge request reached Core")
	}
}

func TestConsoleAPIKeyBridgeRequiresPairedManagement(t *testing.T) {
	c := accountConsoleConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unpaired management reached Core") }))
	c.adminToken = ""
	h := accountConsole(t, c)
	cookie := setupAccount(t, h)
	if w := authRequest(h, "GET", consoleAPIKeysPath, "", cookie); w.Code != 503 {
		t.Fatal("unpaired console offered key management")
	}
	if w := authRequest(h, "GET", "/console/config", "", cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"api_keys":false`) {
		t.Fatal("unpaired key capability was not reported")
	}
}
