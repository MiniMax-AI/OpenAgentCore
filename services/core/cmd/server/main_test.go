package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheckProbesCoreListener(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
		}
	}))
	_, port, _ := net.SplitHostPort(core.Listener.Addr().String())
	// The image listens on every address; the probe uses loopback.
	if err := healthcheck(":" + port); err != nil {
		t.Fatalf("Core was reported unhealthy while it answers: %v", err)
	}
	core.Close()
	if err := healthcheck(":" + port); err == nil {
		t.Fatal("Core was reported healthy after its listener stopped")
	}
}
