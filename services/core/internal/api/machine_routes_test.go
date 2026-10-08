package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/nativeinstaller"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeenrollment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

// machineStore authenticates one fixture host and rejects all executor keys.
// Unexpected state reads fail: rejected credentials must precede those reads.
type machineStore struct {
	t     *testing.T
	calls int
}

func (s *machineStore) GetDeviceCredential(_ context.Context, id string) (runtimedevice.Credential, bool, error) {
	s.calls++
	return runtimedevice.Credential{ID: "host", Type: runtimedevice.RuntimeTypeAgentDaemon, CredentialHash: runtimedevice.HashCredential("host-key")}, id == "host", nil
}
func (s *machineStore) EnrollRuntime(context.Context, string, string) (sandboxbootstrap.Resource, error) {
	s.calls++
	return sandboxbootstrap.Resource{}, sessions.ErrNotFound
}
func (s *machineStore) AuthenticateEnvironmentExecutor(context.Context, string, string) (string, error) {
	s.calls++
	return "", sessions.ErrNotFound
}
func (s *machineStore) GetEnvironment(context.Context, string, string) (sessions.Environment, error) {
	s.t.Fatal("unauthorized environment read")
	return sessions.Environment{}, nil
}
func (s *machineStore) GetEnvironmentResource(context.Context, string, string) (runtimedevice.ServeAuthority, error) {
	s.t.Fatal("unauthorized resource read")
	return runtimedevice.ServeAuthority{}, nil
}

func TestMachineRoutesPreserveAuthorityAndMethodPrecedence(t *testing.T) {
	deps, fakes := testDependencies(t)
	store := &machineStore{t: t}
	links := relay.New(sandboxlinktest.NewAuthority())
	defer links.Close()
	registry := runtimegateway.NewRegistry()
	defer registry.CloseConnections()
	gateway := runtimegateway.NewHandler(runtimegateway.HandlerConfig{Authenticator: runtimegateway.NewAuthenticator(store), Registry: registry, PublicWSURL: testExecutorURL})
	origin, err := deployment.NewPublicOrigin("https://core.example")
	if err != nil {
		t.Fatal(err)
	}
	deps.Execution.Bootstrap, deps.Execution.RuntimeConnect = http.HandlerFunc(gateway.Bootstrap), http.HandlerFunc(gateway.WS)
	deps.Execution.Enrollment = runtimeenrollment.EnrollmentHandler(store, origin)
	deps.Execution.Connection = &runtimeenrollment.Connections{Store: store, Links: links}
	deps.Execution.Links = links
	deps.Execution.NativeInstaller = &NativeInstaller{Version: "build", Base: "https://core.example/api/v1/agent-daemon/install/", Catalog: &nativeinstaller.Catalog{Version: "build", Artifacts: map[string]nativeinstaller.Artifact{"linux-amd64": {}}}}
	nodeID := uuid.NewString()
	hub := node.NewHub(node.HubOptions{Authenticate: func(_ context.Context, id, credential string) (node.Identity, error) {
		store.calls++
		if credential != "node-key" {
			return node.Identity{}, node.ErrAuthentication
		}
		return node.Identity{NodeID: id}, nil
	}, OwnerEpoch: func(context.Context) (uint64, error) { return 1, nil }})
	defer hub.Close()
	deps.Sandboxes.NodeConnect = hub
	fakes.deployment.nodeConfiguration = func(context.Context, string, string, uint64) (deployment.NodeConfiguration, error) {
		return deployment.NodeConfiguration{}, deployment.ErrNodeCredential
	}
	fakes.deployment.nodeStatus = func(context.Context, string, string) (deployment.NodeStatus, error) {
		return deployment.NodeStatus{}, deployment.ErrNodeCredential
	}
	fakes.deployment.enroll = func(context.Context, string, deployment.Enrollment) (deployment.NodeIdentity, error) {
		return deployment.NodeIdentity{}, deployment.ErrNodeCredential
	}
	fakes.environments.validateEnvironmentInstallation = func(context.Context, string, string) (sessions.InstallationAuthorization, error) {
		return sessions.InstallationAuthorization{}, sessions.ErrInstallationAuthorization
	}
	handler := newTestHandler(t, deps)
	for _, tc := range []struct {
		name, method, path, authorization, body string
		status, calls                           int
		allow                                   string
	}{
		{"node configuration missing", "GET", "/sandbox-node/configuration", "", "", 401, 0, ""},
		{"node configuration wrong", "GET", "/sandbox-node/configuration", "Bearer wrong", "", 401, 0, ""},
		{"node identity wrong", "GET", "/sandbox-node/identity?node_id=" + nodeID, "Bearer wrong", "", 401, 0, ""},
		{"node enroll credential before body", "POST", "/sandbox-node/enroll", "", "{", 401, 0, ""},
		{"node enroll wrong", "POST", "/sandbox-node/enroll", "Bearer wrong", `{"core_url":"https://core.example"}`, 401, 0, ""},
		{"node handshake wrong", "GET", "/sandbox-node/connect?node_id=" + nodeID, "Bearer wrong", "", 401, 1, ""},
		{"node handshake invalid", "GET", "/sandbox-node/connect?node_id=" + nodeID, "Bearer node-key", "", 400, 1, ""},
		{"node HEAD never authenticates", "HEAD", "/sandbox-node/connect?node_id=" + nodeID, "Bearer node-key", "", 503, 0, ""},
		{"node POST preserves status", "POST", "/sandbox-node/connect", "", "", 503, 0, ""},
		{"node extension stops before authentication", "PROPFIND", "/sandbox-node/connect?node_id=" + nodeID, "Bearer node-key", "", 405, 0, ""},
		{"bootstrap credential before body", "POST", "/agent-daemon/bootstrap", "", "{", 401, 0, ""},
		{"bootstrap wrong credential", "POST", "/agent-daemon/bootstrap", "Bearer wrong", `{"device_id":"host"}`, 401, 1, ""},
		{"bootstrap malformed", "POST", "/agent-daemon/bootstrap", "Bearer host-key", "{", 400, 0, ""},
		{"bootstrap HEAD", "HEAD", "/agent-daemon/bootstrap", "Bearer host-key", "", 405, 0, "POST"},
		{"runtime missing", "GET", "/agent-daemon/ws", "", "", 400, 0, ""},
		{"runtime wrong", "GET", "/agent-daemon/ws?device_id=host&version=" + proto.Version, "Bearer wrong", "", 401, 1, ""},
		{"runtime handshake", "GET", "/agent-daemon/ws?device_id=host&version=" + proto.Version, "Bearer host-key", "", 400, 1, ""},
		{"runtime version", "GET", "/agent-daemon/ws?device_id=host&version=invalid", "Bearer host-key", "", 426, 0, ""},
		{"runtime HEAD", "HEAD", "/agent-daemon/ws?device_id=host&version=" + proto.Version, "Bearer host-key", "", 405, 0, "GET"},
		{"enroll missing before body", "POST", "/agent-daemon/enroll", "", "{", 401, 0, ""},
		{"enroll wrong", "POST", "/agent-daemon/enroll", "Bearer wrong", `{"environment_id":"environment"}`, 401, 1, ""},
		{"enroll oversized", "POST", "/agent-daemon/enroll", "Bearer wrong", `{"environment_id":"` + strings.Repeat("x", 4096) + `"}`, 400, 0, ""},
		{"enroll query", "POST", "/agent-daemon/enroll?x=1", "Bearer wrong", `{"environment_id":"environment"}`, 400, 0, ""},
		{"enroll HEAD", "HEAD", "/agent-daemon/enroll", "", "", 405, 0, "POST"},
		{"connection missing", "GET", "/agent-daemon/connection", "", "", 401, 0, ""},
		{"connection wrong", "GET", "/agent-daemon/connection?environment_id=environment", "Bearer wrong", "", 401, 1, ""},
		{"connection HEAD", "HEAD", "/agent-daemon/connection?environment_id=environment", "Bearer wrong", "", 405, 0, "GET"},
		{"installation missing", "POST", "/agent-daemon/installation", "", "", 401, 0, ""},
		{"installation wrong", "POST", "/agent-daemon/installation", "Bearer wrong", "", 401, 0, ""},
		{"claim missing before body", "POST", "/agent-daemon/installation/claim", "", "{", 401, 0, ""},
		{"claim wrong", "POST", "/agent-daemon/installation/claim", "Bearer wrong", "{", 401, 0, ""},
		{"installer missing", "GET", "/agent-daemon/install/build/no-file", "", "", 404, 0, ""},
		{"installer wrong method", "POST", "/agent-daemon/install/build/bootstrap.sh", "", "", 405, 0, ""},
		{"link handshake", "GET", "/sandbox-link", "", "", 400, 0, ""},
		{"link HEAD", "HEAD", "/sandbox-link", "", "", 405, 0, "GET"},
		{"unknown daemon", "GET", "/agent-daemon/missing", "", "", 404, 0, ""},
		{"encoded route separator", "GET", "/agent-daemon%2Fconnection", "Bearer wrong", "", 404, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store.calls = 0
			request := httptest.NewRequest(tc.method, "/api/v1"+tc.path, strings.NewReader(tc.body))
			request.Header.Set("Authorization", tc.authorization)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			// Extension rejection belongs to the router; its Allow list does not
			// declare the node handler's supported operations.
			if response.Code != tc.status || store.calls != tc.calls || tc.method != "PROPFIND" && response.Header().Get("Allow") != tc.allow {
				t.Fatalf("status/calls/Allow = %d/%d/%q; want %d/%d/%q; %s", response.Code, store.calls, response.Header().Get("Allow"), tc.status, tc.calls, tc.allow, response.Body)
			}
			var envelope v1.ErrorResponse
			if response.Header().Get("Content-Type") != "application/json" || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Error.Type == "" {
				t.Fatalf("non-machine error: %s", response.Body)
			}
			if strings.Contains(tc.name, "handshake") && tc.status == 400 && response.Header().Get("Sec-Websocket-Version") != "13" {
				t.Fatal("WebSocket version response header lost")
			}
			if strings.Contains(response.Body.String(), "host-key") || strings.Contains(response.Body.String(), "node-key") {
				t.Fatal("credential leaked")
			}
		})
	}
	// An otherwise valid WebSocket request still respects the node and Link
	// origin checks, and preserves the SDK's failure headers.
	for _, tc := range []struct {
		path, credential string
		calls            int
	}{
		{"/sandbox-node/connect?node_id=" + nodeID, "Bearer node-key", 1},
		{"/sandbox-link", "", 0},
	} {
		store.calls = 0
		request := httptest.NewRequest(http.MethodGet, "/api/v1"+tc.path, nil)
		request.Header = http.Header{"Authorization": {tc.credential}, "Connection": {"Upgrade"}, "Upgrade": {"websocket"}, "Sec-Websocket-Version": {"13"}, "Sec-Websocket-Key": {"dGhlIHNhbXBsZSBub25jZQ=="}, "Origin": {"https://foreign.example"}}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var envelope v1.ErrorResponse
		if response.Code != 403 || store.calls != tc.calls || response.Header().Get("Sec-Websocket-Version") != "13" || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Error.Message != http.StatusText(403) {
			t.Fatalf("origin check %s: %v", tc.path, response)
		}
	}
	for _, prefix := range []string{"/api/v1/agent-daemon", "/api/v1/agent-daemon/install"} {
		for _, method := range []string{"GET", "POST", "HEAD"} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(method, prefix+"?keep=query", nil))
			if response.Code != 301 || response.Header().Get("Location") != prefix+"/?keep=query" {
				t.Fatalf("prefix redirect %s: %v", method, response)
			}
		}
	}
}
