package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type bootstrapCredentialStore struct {
	nodeID       string
	allocationID string
}

func (s bootstrapCredentialStore) GetDeviceCredential(context.Context, string) (device.Credential, bool, error) {
	return device.Credential{ID: "runtime", Type: gateway.RuntimeTypeAgentDaemon,
		CredentialHash: device.HashCredential("synthetic-token"), RuntimeNodeID: s.nodeID, RuntimeAllocationID: s.allocationID}, true, nil
}

func TestBootstrapAddressFollowsAuthenticatedAllocation(t *testing.T) {
	const publicURL = "wss://private-proxy.example/api/v1/agent-daemon/ws"
	local := &managedNodes{runtime: &execution.RuntimeProvider{LocalNodeID: "local-node", CoreURL: "http://host.microsandbox.internal:8091/api/v1"}}
	remote := &managedNodes{setup: &managedSetup{store: &setupStore{value: store.SandboxSetup{Provider: "docker", Generation: 1}}}}
	remote.setup.selected.Store(&execution.RuntimeProvider{CoreURL: "https://selected-node-entry.example/api/v1", Generation: 1})
	zero := &managedNodes{setup: &managedSetup{}}
	for _, tc := range []struct {
		name, node, want string
		managed          *managedNodes
	}{
		{"embedded managed", "local-node", "ws://host.microsandbox.internal:8091/api/v1/agent-daemon/ws", local},
		{"remote managed beside embedded node", "remote-node", publicURL, local},
		{"self-hosted beside embedded node", "", publicURL, local},
		// Every placed daemon uses the one address derived from the public URL.
		{"remote managed after Web setup", "remote-node", publicURL, remote},
		{"self-hosted after Web setup", "", publicURL, remote},
		{"self-hosted before Web setup", "", publicURL, zero},
		{"standalone self-hosted", "", publicURL, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := gateway.NewHandler(gateway.HandlerConfig{Registry: gateway.NewRegistry(), PublicWSURL: publicURL,
				Authenticator: gateway.NewAuthenticator(bootstrapCredentialStore{nodeID: tc.node}),
				ResolveWSURL:  tc.managed.webSocketURL(publicURL)})
			request := httptest.NewRequest(http.MethodPost, "https://forged.example/api/v1/agent-daemon/bootstrap",
				strings.NewReader(`{"device_id":"runtime","node_id":"local-node","runtime_node_id":"local-node"}`))
			request.Header.Set("Authorization", "Bearer synthetic-token")
			request.Header.Set("X-Forwarded-Host", "forged-proxy.example")
			response := httptest.NewRecorder()
			h.Bootstrap(response, request)
			var body map[string]any
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body["ws_url"] != tc.want {
				t.Fatalf("bootstrap status=%d body=%s", response.Code, response.Body.String())
			}
			if len(body) != 5 {
				t.Fatal("bootstrap exposed private allocation metadata")
			}
		})
	}
}

func TestEmbeddedBootstrapDoesNotFallbackWhenInternalAddressIsInvalid(t *testing.T) {
	managed := &managedNodes{runtime: &execution.RuntimeProvider{LocalNodeID: "local-node", CoreURL: "invalid"}}
	h := gateway.NewHandler(gateway.HandlerConfig{Registry: gateway.NewRegistry(), PublicWSURL: "wss://public.example/api/v1/agent-daemon/ws",
		Authenticator: gateway.NewAuthenticator(bootstrapCredentialStore{nodeID: "local-node"}),
		ResolveWSURL:  managed.webSocketURL("wss://public.example/api/v1/agent-daemon/ws")})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent-daemon/bootstrap", strings.NewReader(`{"device_id":"runtime"}`))
	request.Header.Set("Authorization", "Bearer synthetic-token")
	response := httptest.NewRecorder()
	h.Bootstrap(response, request)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "public.example") {
		t.Fatalf("bootstrap fell back after route failure: %d %s", response.Code, response.Body.String())
	}
}
