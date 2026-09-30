package runtimeenrollment

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

type connectionStub struct {
	err   error
	calls int
}

func (s *connectionStub) AuthenticateEnvironmentExecutor(_ context.Context, environment, digest string) (string, error) {
	s.calls++
	if environment != "environment" || digest != device.HashCredential("test-key") {
		return "", store.ErrNotFound
	}
	return "tenant", s.err
}
func (*connectionStub) GetEnvironment(context.Context, string, string) (store.Environment, error) {
	return store.Environment{ID: "environment", SessionID: "session", Status: "pending"}, nil
}
func (*connectionStub) GetSessionDevice(context.Context, string, string) (store.ExecutionDevice, error) {
	return store.ExecutionDevice{}, store.ErrNotFound
}
func (*connectionStub) GetDeviceCredential(context.Context, string) (device.Credential, bool, error) {
	panic("unbound lookup")
}

func TestConnectionReadContract(t *testing.T) {
	for _, tc := range []struct {
		method, query, bearer string
		err                   error
		code, calls           int
	}{
		{"GET", "environment_id=environment", "Bearer test-key", nil, 200, 1},
		{"GET", "environment_id=environment", "", nil, 401, 0},
		{"POST", "environment_id=environment", "Bearer test-key", nil, 405, 0},
		{"GET", "environment_id=environment&environment_id=other", "Bearer test-key", nil, 400, 0},
		{"GET", "environment_id=environment&other=1", "Bearer test-key", nil, 400, 0},
		{"GET", "environment_id=%zz", "Bearer test-key", nil, 400, 0},
		{"GET", "environment_id=environment", "Bearer test-key", store.ErrNotFound, 401, 1},
		{"GET", "environment_id=environment", "Bearer test-key", errors.New("private detail"), 503, 1},
	} {
		s := &connectionStub{err: tc.err}
		req := httptest.NewRequest(tc.method, "/api/v1/agent-daemon/connection?"+tc.query, nil)
		req.Header.Set("Authorization", tc.bearer)
		res := httptest.NewRecorder()
		ConnectionHandler(s, gateway.NewRegistry()).ServeHTTP(res, req)
		if res.Code != tc.code || s.calls != tc.calls || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: %d, %d calls", tc.method, tc.query, res.Code, s.calls)
		}
		if strings.Contains(res.Body.String(), "private") || strings.Contains(res.Body.String(), "test-key") {
			t.Fatal("private data exposed")
		}
		if res.Code == 200 && res.Body.String() != `{"environment_id":"environment","status":"disconnected"}`+"\n" {
			t.Fatal("unexpected response", res.Body.String())
		}
	}
}

// A real gateway peer captures its digest at HTTP upgrade. The test store makes
// authority changes at the deterministic post-peer recheck, without timing sleeps.
type liveConnectionStore struct {
	digest                 string
	authCalls              int
	credentialCalls        int
	revokeAtRecheck        bool
	deviceRevokedAtRecheck bool
	recheckError           error
	credentialRecheckError error
}

func (s *liveConnectionStore) AuthenticateEnvironmentExecutor(context.Context, string, string) (string, error) {
	s.authCalls++
	if s.authCalls == 2 {
		if s.recheckError != nil {
			return "", s.recheckError
		}
		if s.revokeAtRecheck {
			return "", store.ErrNotFound
		}
	}
	return "tenant", nil
}
func (s *liveConnectionStore) GetEnvironment(context.Context, string, string) (store.Environment, error) {
	return store.Environment{ID: "environment", SessionID: "session", Status: "connected"}, nil
}
func (s *liveConnectionStore) GetSessionDevice(context.Context, string, string) (store.ExecutionDevice, error) {
	return store.ExecutionDevice{ID: "device", EnvironmentID: "environment"}, nil
}
func (s *liveConnectionStore) GetDeviceCredential(context.Context, string) (device.Credential, bool, error) {
	s.credentialCalls++
	if s.authCalls >= 2 && s.credentialRecheckError != nil {
		return device.Credential{}, false, s.credentialRecheckError
	}
	if s.deviceRevokedAtRecheck && s.authCalls >= 2 {
		return device.Credential{}, false, nil
	}
	return device.Credential{ID: "device", WorkspaceID: "tenant", Type: gateway.RuntimeTypeAgentDaemon, CredentialHash: s.digest}, true, nil
}
func TestRuntimeConnectedCurrentAuthorityAfterPeer(t *testing.T) {
	for _, name := range []string{"connected", "rotated before read", "revoked after peer", "device revoked after peer", "retired after peer", "store error after peer", "device store error after peer", "closed", "no registry"} {
		t.Run(name, func(t *testing.T) {
			digest := device.HashCredential("fixture-key")
			s := &liveConnectionStore{digest: digest}
			registry := gateway.NewRegistry()
			handler := gateway.NewHandler(gateway.HandlerConfig{Registry: registry, Authenticator: gateway.NewAuthenticator(s)})
			server := httptest.NewServer(http.HandlerFunc(handler.WS))
			defer server.Close()
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?device_id=device&version="+proto.Version, http.Header{"Authorization": {"Bearer fixture-key"}})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			// Wait for the actual registration, not merely the transport upgrade.
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			peer, err := registry.WaitForDevice(ctx, "device", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close("test complete")
			s.authCalls = 0
			var wantErr error
			want := name == "connected"
			switch name {
			case "rotated before read":
				s.digest = device.HashCredential("new-key")
				digest = s.digest
			case "revoked after peer":
				s.revokeAtRecheck = true
				wantErr = store.ErrNotFound
			case "device revoked after peer", "retired after peer":
				s.deviceRevokedAtRecheck = true
				wantErr = store.ErrNotFound
			case "store error after peer":
				s.recheckError = errors.New("database unavailable")
				wantErr = s.recheckError
			case "device store error after peer":
				s.credentialRecheckError = errors.New("device authority unavailable")
				wantErr = s.credentialRecheckError
			case "closed":
				peer.Close("closed before observation")
			case "no registry":
				registry = nil
			}
			connected, err := RuntimeConnected(t.Context(), s, registry, "environment", digest)
			if connected != want || !errors.Is(err, wantErr) {
				t.Fatalf("connected=%v err=%v", connected, err)
			}
			if name == "connected" && s.authCalls != 2 {
				t.Fatal("post-peer authority was not checked")
			}
		})
	}
}
