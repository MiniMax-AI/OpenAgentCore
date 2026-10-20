package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHealthcheckProbesOnlyWeb(t *testing.T) {
	directory := t.TempDir()
	key := filepath.Join(directory, "core.key")
	if err := os.WriteFile(key, []byte(strings.Repeat("k", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
		}
	}))
	_, port, _ := net.SplitHostPort(web.Listener.Addr().String())
	t.Setenv("OAC_WEB_CORE_KEY_FILE", key)
	t.Setenv("OAC_PUBLIC_URL", testOrigin)
	t.Setenv("OAC_WEB_DIST", directory)
	t.Setenv("OAC_WEB_ADDR", ":"+port)
	// Nothing listens here: Core is down, and Web stays healthy.
	t.Setenv("OAC_WEB_UPSTREAM", "http://127.0.0.1:1")
	if err := healthcheck(); err != nil {
		t.Fatalf("Web was reported unhealthy while it answers: %v", err)
	}
	web.Close()
	if err := healthcheck(); err == nil {
		t.Fatal("Web was reported healthy after its listener stopped")
	}
}
