package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/google/uuid"
)

func TestWebSetupCreatesManagerWithoutLocalProvider(t *testing.T) {
	t.Setenv("AGENTS_API_MANAGED_RUNTIMES_FILE", "")
	t.Setenv("AGENTS_API_SANDBOX_INSTALLATION_ID", uuid.NewString())
	t.Setenv("AGENTS_API_DAEMON_WS_URL", "ws://core:8091/api/v1/agent-daemon/ws")
	digest := sha256.Sum256([]byte("synthetic-admin"))
	path := filepath.Join(t.TempDir(), "digests.json")
	if err := os.WriteFile(path, []byte(`["`+hex.EncodeToString(digest[:])+`"]`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE", path)
	m, err := configureManagedNodes(nil, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer m.close()
	if m.setup == nil || m.admin == nil || m.hub == nil || m.runtime == nil || m.local != nil || m.runtime.Provider != nil {
		t.Fatal("zero-node setup unexpectedly instantiated local compute or omitted management")
	}
	t.Setenv("AGENTS_API_SANDBOX_ADMIN_DIGESTS_FILE", "")
	if _, err := configureManagedNodes(nil, nil); err == nil {
		t.Fatal("setup accepted without admin authentication")
	}
}

func TestSelectedSetupPublishesSameOriginForDaemonBootstrap(t *testing.T) {
	for _, tc := range []struct{ origin, want string }{
		{"https://core.example:8443/api/v1", "wss://core.example:8443/api/v1/agent-daemon/ws"},
		{"http://127.0.0.1:8091/api/v1", "ws://127.0.0.1:8091/api/v1/agent-daemon/ws"},
	} {
		s := &managedSetup{}
		s.selected.Store(&execution.RuntimeProvider{CoreURL: tc.origin, ProviderKind: "docker"})
		got, err := s.webSocketURL("ws://private-core:8091/api/v1/agent-daemon/ws")(context.Background())
		if err != nil || got != tc.want {
			t.Fatalf("bootstrap URL=%s error=%v", got, err)
		}
		if s.ObservationProviderType() != "docker" {
			t.Fatal("observation lost selected provider")
		}
	}
}
