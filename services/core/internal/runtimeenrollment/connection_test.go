package runtimeenrollment

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

type connectionStub struct {
	err   error
	calls int
}

func (s *connectionStub) AuthenticateEnvironmentExecutor(_ context.Context, environment, digest string) (string, error) {
	s.calls++
	if environment != "environment" || digest != runtimedevice.HashCredential("test-key") {
		return "", sessions.ErrNotFound
	}
	return "tenant", s.err
}
func (*connectionStub) GetEnvironment(context.Context, string, string) (sessions.Environment, error) {
	return sessions.Environment{ID: "environment", SessionID: "session", Status: "pending"}, nil
}
func (*connectionStub) GetEnvironmentResource(context.Context, string, string) (runtimedevice.ServeAuthority, error) {
	return runtimedevice.ServeAuthority{}, sessions.ErrNotFound
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
		{"GET", "environment_id=environment", "Bearer test-key", sessions.ErrNotFound, 401, 1},
		{"GET", "environment_id=environment", "Bearer test-key", errors.New("private detail"), 503, 1},
	} {
		s := &connectionStub{err: tc.err}
		req := httptest.NewRequest(tc.method, "/api/v1/agent-daemon/connection?"+tc.query, nil)
		req.Header.Set("Authorization", tc.bearer)
		res := httptest.NewRecorder()
		(&Connections{Store: s, Links: relay.New(sandboxlinktest.NewAuthority())}).ServeHTTP(res, req)
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

// The test store changes authority at the post-relay recheck, the second
// authentication, without timing sleeps.
type liveConnectionStore struct {
	resource        sandboxbootstrap.Resource
	digest          string
	authCalls       int
	revokeAtRecheck bool
	rotateAtRecheck bool
	recheckError    error
}

func (s *liveConnectionStore) AuthenticateEnvironmentExecutor(context.Context, string, string) (string, error) {
	s.authCalls++
	if s.authCalls == 2 {
		if s.recheckError != nil {
			return "", s.recheckError
		}
		if s.revokeAtRecheck {
			return "", sessions.ErrNotFound
		}
	}
	return s.resource.TenantID, nil
}
func (s *liveConnectionStore) GetEnvironment(context.Context, string, string) (sessions.Environment, error) {
	return sessions.Environment{ID: s.resource.EnvironmentID, SessionID: "session", Status: "connected"}, nil
}
func (s *liveConnectionStore) GetEnvironmentResource(context.Context, string, string) (runtimedevice.ServeAuthority, error) {
	resource := s.resource
	if s.rotateAtRecheck && s.authCalls >= 2 {
		resource.Generation++
	}
	return runtimedevice.ServeAuthority{Resource: resource, CredentialHash: s.digest}, nil
}

// Connected follows the enrolled sandbox's serve peer at the relay and the
// credential's authority after reading it.
func TestRuntimeConnectedFollowsServeAndCurrentAuthority(t *testing.T) {
	resource := sandboxbootstrap.Resource{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), Kind: "enrollment", ID: uuid.NewString(), Generation: 1}
	auth := sandboxlinktest.NewAuthority()
	auth.AddServe([]byte("fixture-key"), sandboxlink.ServePeer{PeerID: sandboxwire.NewID(), Resource: resource.Ref()})
	srv := sandboxlinktest.StartRelay(t, auth)
	ctx, cancel := context.WithCancel(t.Context())
	connected := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		hold := func(ctx context.Context, _ sandboxlink.Bind, _ uint64, _ sandboxlink.Stream) { <-ctx.Done() }
		_ = sandboxlink.Serve(ctx, sandboxlink.ServeConfig{URL: srv.URL, TLS: srv.TLS, Credential: []byte("fixture-key"), Resource: resource.Ref(), ServerInstanceID: sandboxwire.NewID(),
			Services:    []sandboxlink.ServiceHandler{{Service: sandboxlink.ServiceFile, Version: 1, Serve: hold}},
			OnConnected: func() { connected <- struct{}{} }, MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond})
	}()
	defer func() { cancel(); <-done }()
	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("the sandbox did not serve")
	}
	for !srv.Relay.Serving(resource.Ref()) {
		time.Sleep(10 * time.Millisecond)
	}
	digest := runtimedevice.HashCredential("fixture-key")
	unavailable := errors.New("database unavailable")
	for _, tc := range []struct {
		name  string
		store liveConnectionStore
		want  bool
		err   error
	}{
		{name: "connected", store: liveConnectionStore{resource: resource, digest: digest}, want: true},
		{name: "another credential", store: liveConnectionStore{resource: resource, digest: runtimedevice.HashCredential("other-key")}, err: sessions.ErrDeviceBindingConflict},
		{name: "not served", store: liveConnectionStore{resource: withGeneration(resource, 2), digest: digest}},
		{name: "revoked after relay", store: liveConnectionStore{resource: resource, digest: digest, revokeAtRecheck: true}, err: sessions.ErrNotFound},
		{name: "rotated after relay", store: liveConnectionStore{resource: resource, digest: digest, rotateAtRecheck: true}},
		{name: "store error after relay", store: liveConnectionStore{resource: resource, digest: digest, recheckError: unavailable}, err: unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.store
			got, err := (&Connections{Store: &s, Links: srv.Relay}).ExecutorConnected(t.Context(), resource.EnvironmentID, digest)
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("connected=%v err=%v", got, err)
			}
			if tc.name == "connected" && s.authCalls != 2 {
				t.Fatal("authority was not rechecked after the relay")
			}
		})
	}
}

func withGeneration(resource sandboxbootstrap.Resource, generation uint64) sandboxbootstrap.Resource {
	resource.Generation = generation
	return resource
}
