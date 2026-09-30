package runtimegateway

import (
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBootstrapPublishesURLOnlyAfterAuthentication(t *testing.T) {
	const token = "synthetic-bootstrap-credential"
	const publicURL = "wss://core.example/api/v1/agent-daemon/ws"
	for _, valid := range []bool{true, false} {
		h := NewHandler(HandlerConfig{Registry: NewRegistry(), PublicWSURL: publicURL,
			Authenticator: NewAuthenticator(&stubRuntimeStore{ok: true, row: runtimedevice.Credential{ID: "device", WorkspaceID: "tenant", Type: RuntimeTypeAgentDaemon, CredentialHash: runtimedevice.HashCredential(token)}})})
		r := httptest.NewRequest(http.MethodPost, "/agent-daemon/bootstrap", strings.NewReader(`{"device_id":"device"}`))
		bearer, status := "wrong", http.StatusUnauthorized
		if valid {
			bearer, status = token, http.StatusOK
		}
		r.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		h.Bootstrap(w, r)
		if w.Code != status || strings.Contains(w.Body.String(), publicURL) != valid {
			t.Fatalf("bootstrap status=%d body=%s", w.Code, w.Body.String())
		}
	}
}
