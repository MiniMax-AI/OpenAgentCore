package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/node"
	"github.com/google/uuid"
)

// Only Core's rejection of the node credential stops the node service for good:
// exit 78, which the unit's RestartPreventExitStatus honors. Every other failure
// exits 1, so systemd restarts the node.
func TestExitCodeStopsTheNodeOnlyForARejectedCredential(t *testing.T) {
	answering := func(status int) string {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		t.Cleanup(server.Close)
		return server.URL
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{"credential rejected", refreshIdentity(t, answering(http.StatusUnauthorized)), exitRejected},
		{"proxy forbids", refreshIdentity(t, answering(http.StatusForbidden)), 1},
		{"Core unavailable", refreshIdentity(t, answering(http.StatusServiceUnavailable)), 1},
		{"Core unreachable", refreshIdentity(t, closed.URL), 1},
		{"specification changed", errors.New("provider configuration differs from retained node identity"), 1},
	} {
		if test.err == nil {
			t.Fatal(test.name, "refresh succeeded")
		}
		if got := exitCode(test.err); got != test.want {
			t.Errorf("%s: exit %d, want %d (%v)", test.name, got, test.want, test.err)
		}
	}
}

// refreshIdentity runs the node's startup identity refresh against coreURL.
func refreshIdentity(t *testing.T, coreURL string) error {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	identity := node.Identity{InstallationID: uuid.NewString(), Provider: "docker", BackendFingerprint: strings.Repeat("1", 64)}
	if _, err := node.InitIdentity(dir, coreURL, identity); err != nil {
		t.Fatal(err)
	}
	_, err := node.RefreshIdentity(t.Context(), dir)
	return err
}

func TestNodeRejectsRetiredLoggingSettingsBeforeStartup(t *testing.T) {
	t.Setenv("PARSAR_LOG_LEVEL", "private-log")
	t.Setenv("PARSAR_LOG_FORMAT", "")
	err := run(t.Context(), []string{"run"})
	if err == nil || !strings.Contains(err.Error(), "PARSAR_LOG_LEVEL → OAC_LOG_LEVEL") || !strings.Contains(err.Error(), "PARSAR_LOG_FORMAT → OAC_LOG_FORMAT") || strings.Contains(err.Error(), "private-log") {
		t.Fatalf("retired logging settings were ignored or exposed: %v", err)
	}
}
