package runtimeenrollment

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

type enrollmentStub struct {
	calls int
	err   error
}

func (s *enrollmentStub) EnrollRuntime(_ context.Context, environment, digest string) (store.RuntimeEnrollment, error) {
	s.calls++
	if environment != "environment" || digest != runtimedevice.HashCredential("private-test-token") {
		return store.RuntimeEnrollment{}, errors.New("unexpected enrollment input")
	}
	return store.RuntimeEnrollment{DeviceID: "device", SessionID: "session", EnvironmentID: environment, WorkspaceDirectory: "/workspace"}, s.err
}

func TestEnrollmentConnectionContract(t *testing.T) {
	for _, test := range []struct {
		name, body, authorization string
		err                       error
		status, calls             int
	}{
		{"valid", `{"environment_id":"environment"}`, "Bearer private-test-token", nil, 200, 1},
		{"missing authority", `{"environment_id":"environment"}`, "", nil, 401, 0},
		{"caller binding", `{"environment_id":"environment","session_id":"other"}`, "Bearer private-test-token", nil, 400, 0},
		{"extra input", `{"environment_id":"environment"}{}`, "Bearer private-test-token", nil, 400, 0},
		{"foreign", `{"environment_id":"environment"}`, "Bearer private-test-token", sessions.ErrNotFound, 401, 1},
		{"conflict", `{"environment_id":"environment"}`, "Bearer private-test-token", sessions.ErrDeviceBindingConflict, 409, 1},
		{"internal failure", `{"environment_id":"environment"}`, "Bearer private-test-token", errors.New("private database detail"), 503, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &enrollmentStub{err: test.err}
			req := httptest.NewRequest("POST", "/api/v1/agent-daemon/enroll", strings.NewReader(test.body))
			req.Header.Set("Authorization", test.authorization)
			response := httptest.NewRecorder()
			EnrollmentHandler(s).ServeHTTP(response, req)
			if response.Code != test.status || s.calls != test.calls {
				t.Fatalf("status/calls %d/%d", response.Code, s.calls)
			}
			if strings.Contains(response.Body.String(), "private") {
				t.Fatal("enrollment leaked confidential details")
			}
			if response.Code == 200 && (response.Body.String() != `{"device_id":"device","session_id":"session","environment_id":"environment","workspace_directory":"/workspace"}`+"\n" || response.Header().Get("Cache-Control") != "no-store") {
				t.Fatalf("binding response %s", response.Body.String())
			}
		})
	}
}
