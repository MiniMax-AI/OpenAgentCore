package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBootstrapAcceptsOnlySameOriginLiteralIP(t *testing.T) {
	h := &console{config: config{origin: "http://127.0.0.1:8080", bootstrap: true}, host: "127.0.0.1:8080"}
	for _, tc := range []struct {
		host, origin string
		want         bool
	}{
		{"203.0.113.10:8080", "http://203.0.113.10:8080", true},
		{"[2001:db8::1]:8080", "http://[2001:db8::1]:8080", true},
		{"evil.example:8080", "http://evil.example:8080", false},
		{"203.0.113.10:8080", "http://evil.example:8080", false},
		{"203.0.113.10:8080", "https://203.0.113.10:8080", false},
		{"203.0.113.10:99999", "http://203.0.113.10:99999", false},
	} {
		t.Run(tc.host+tc.origin, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://localhost/console/installation/domain", nil)
			r.Host = tc.host
			r.Header.Set("Origin", tc.origin)
			if got := h.sameOrigin(r); got != tc.want {
				t.Fatalf("sameOrigin=%v, want %v", got, tc.want)
			}
		})
	}
	h.bootstrap = false
	h.origin = "https://core.example"
	h.host = "core.example"
	r := httptest.NewRequest(http.MethodGet, "http://203.0.113.10:8080/", nil)
	if h.sameOrigin(r) {
		t.Fatal("bootstrap IP still accepted after HTTPS setup")
	}
}

func TestInstallationDomainRequiresConsoleSessionAndUsesOnlyPrivateSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "manager.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	manager := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/domain" || r.Header.Get("Authorization") != "Bearer "+testCoreKey || r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected installer request: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"supported":true,"state":"checking","target_url":"https://core.example"}`)
	})}
	go manager.Serve(listener)
	t.Cleanup(func() { _ = manager.Close() })
	c := coreKeyConsoleConfig(t, http.NotFoundHandler())
	c.installationSocket = socket
	h := startConsole(t, c)
	if w := authRequest(h, "POST", "/console/installation/domain", `{"hostname":"core.example"}`, nil); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	cookie := signIn(t, h)
	r := httptest.NewRequest("POST", "/console/installation/domain", strings.NewReader(`{"hostname":"core.example"}`))
	r.Host = h.host
	r.AddCookie(cookie)
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || calls.Load() != 0 {
		t.Fatal("unauthenticated or cross-site request reached installer")
	}
	w = authRequest(h, "POST", "/console/installation/domain", `{"hostname":"core.example"}`, cookie)
	if w.Code != 202 || calls.Load() != 1 {
		t.Fatalf("authenticated setup: %d %s", w.Code, w.Body)
	}
}
