package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/gateway"
)

type bootstrapCredentialStore struct {
	nodeID       string
	allocationID string
}

func (s bootstrapCredentialStore) GetDeviceCredential(context.Context, string) (device.Credential, bool, error) {
	return device.Credential{ID: "runtime", Type: gateway.RuntimeTypeAgentDaemon,
		CredentialHash: device.HashCredential("synthetic-token"), RuntimeNodeID: s.nodeID, RuntimeAllocationID: s.allocationID}, true, nil
}

func TestBootstrapAddressUsesPublicOrigin(t *testing.T) {
	const publicURL = "wss://public.example/api/v1/agent-daemon/ws"
	for _, node := range []string{"local-node", "remote-node", ""} {
		t.Run(node, func(t *testing.T) {
			h := gateway.NewHandler(gateway.HandlerConfig{Registry: gateway.NewRegistry(), PublicWSURL: publicURL,
				Authenticator: gateway.NewAuthenticator(bootstrapCredentialStore{nodeID: node})})
			request := httptest.NewRequest(http.MethodPost, "https://forged.example/api/v1/agent-daemon/bootstrap",
				strings.NewReader(`{"device_id":"runtime","node_id":"local-node","runtime_node_id":"local-node"}`))
			request.Header.Set("Authorization", "Bearer synthetic-token")
			request.Header.Set("X-Forwarded-Host", "forged-proxy.example")
			response := httptest.NewRecorder()
			h.Bootstrap(response, request)
			var body map[string]any
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body["ws_url"] != publicURL {
				t.Fatalf("bootstrap status=%d body=%s", response.Code, response.Body.String())
			}
			if len(body) != 5 {
				t.Fatal("bootstrap exposed private allocation metadata")
			}
		})
	}
}
