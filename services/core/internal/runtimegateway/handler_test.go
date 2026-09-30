package runtimegateway

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/gorilla/websocket"
)

func TestWebSocketRequiresAuthorizationBearer(t *testing.T) {
	const credential = "synthetic-runtime-credential"
	for _, tc := range []struct {
		name, authorization, queryToken string
		status                          int
	}{
		{"header", "Bearer " + credential, "", http.StatusSwitchingProtocols},
		{"retired wire version", "Bearer " + credential, "", http.StatusUpgradeRequired},
		{"missing header", "", "", http.StatusBadRequest},
		{"query alone", "", credential, http.StatusBadRequest},
		{"wrong scheme", "Basic " + credential, credential, http.StatusBadRequest},
		{"invalid header cannot use query", "Bearer invalid", credential, http.StatusUnauthorized},
		{"header ignores query", "Bearer " + credential, "invalid", http.StatusSwitchingProtocols},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewRegistry()
			handler := NewHandler(HandlerConfig{
				Registry: registry,
				Authenticator: NewAuthenticator(&stubRuntimeStore{ok: true, row: runtimedevice.Credential{
					ID: "device", WorkspaceID: "tenant", Type: RuntimeTypeAgentDaemon,
					CredentialHash: runtimedevice.HashCredential(credential),
				}}),
			})
			server := httptest.NewServer(http.HandlerFunc(handler.WS))
			defer server.Close()
			query := url.Values{"device_id": {"device"}, "version": {proto.Version}}
			if tc.name == "retired wire version" {
				query.Set("version", "0.2.0")
			}
			if tc.queryToken != "" {
				query.Set("token", tc.queryToken)
			}
			endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "?" + query.Encode()
			conn, response, err := websocket.DefaultDialer.Dial(endpoint, http.Header{"Authorization": {tc.authorization}})
			if response == nil {
				t.Fatalf("missing upgrade response: %v", err)
			}
			defer response.Body.Close()
			if conn != nil {
				defer conn.Close()
			}
			if response.StatusCode != tc.status {
				t.Fatalf("upgrade status = %d, want %d", response.StatusCode, tc.status)
			}
			if tc.status == http.StatusSwitchingProtocols && err != nil {
				t.Fatalf("header-authenticated upgrade failed: %v", err)
			}
		})
	}
}
