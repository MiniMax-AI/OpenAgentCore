package runtimeenrollment

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
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
