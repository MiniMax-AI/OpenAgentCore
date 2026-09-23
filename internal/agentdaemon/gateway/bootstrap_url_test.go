package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
)

func TestBootstrapResolvesPublicURLOnlyAfterAuthentication(t *testing.T) {
	const token = "synthetic-bootstrap-credential"
	for _, tc := range []struct {
		name, bearer, result string
		err                  error
		status, calls        int
	}{
		{"selected origin", token, "wss://core.example/api/v1/agent-daemon/ws", nil, 200, 1},
		{"unavailable", token, "", errors.New("private database detail"), 503, 1},
		{"empty origin", token, "", nil, 503, 1},
		{"unauthenticated", "wrong", "", nil, 401, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h := NewHandler(HandlerConfig{Registry: NewRegistry(), PublicWSURL: "ws://private-core:8091/api/v1/agent-daemon/ws",
				Authenticator: NewAuthenticator(&stubRuntimeStore{ok: true, row: device.Credential{ID: "device", WorkspaceID: "tenant", Type: RuntimeTypeAgentDaemon, CredentialHash: device.HashCredential(token)}}),
				ResolveWSURL:  func(context.Context, AuthenticatedRuntime) (string, error) { calls++; return tc.result, tc.err },
			})
			r := httptest.NewRequest(http.MethodPost, "/agent-daemon/bootstrap", strings.NewReader(`{"device_id":"device"}`))
			r.Header.Set("Authorization", "Bearer "+tc.bearer)
			w := httptest.NewRecorder()
			h.Bootstrap(w, r)
			if w.Code != tc.status || calls != tc.calls {
				t.Fatalf("status %d, calls %d", w.Code, calls)
			}
			if strings.Contains(w.Body.String(), "private-") || strings.Contains(w.Body.String(), "private database detail") {
				t.Fatalf("private configuration leaked: %s", w.Body.String())
			}
			if tc.status == 200 && !strings.Contains(w.Body.String(), tc.result) {
				t.Fatalf("selected address missing: %s", w.Body.String())
			}
		})
	}
}
