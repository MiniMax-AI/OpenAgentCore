package runtimeenrollment

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

type enrollmentStub struct {
	calls int
	err   error
}

func (s *enrollmentStub) EnrollRuntime(_ context.Context, environment, digest string) (sandboxbootstrap.Resource, error) {
	s.calls++
	if environment != "environment" || digest != runtimedevice.HashCredential("private-test-token") {
		return sandboxbootstrap.Resource{}, errors.New("unexpected enrollment input")
	}
	return sandboxbootstrap.Resource{TenantID: "tenant", EnvironmentID: environment, Kind: "enrollment", ID: "enrollment", Generation: 2}, s.err
}

func TestEnrollmentConnectionContract(t *testing.T) {
	origin := func(value string) deployment.PublicOrigin {
		o, err := deployment.NewPublicOrigin(value)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	https := origin("https://core.example")
	for _, test := range []struct {
		name, body, authorization string
		origin                    deployment.PublicOrigin
		err                       error
		status, calls             int
	}{
		{"valid", `{"environment_id":"environment"}`, "Bearer private-test-token", https, nil, 200, 1},
		// A self_hosted sandbox dials the Link from its own host.
		{"no wss Link", `{"environment_id":"environment"}`, "Bearer private-test-token", origin("http://127.0.0.1:8080"), nil, 503, 0},
		{"missing authority", `{"environment_id":"environment"}`, "", https, nil, 401, 0},
		{"caller binding", `{"environment_id":"environment","session_id":"other"}`, "Bearer private-test-token", https, nil, 400, 0},
		{"extra input", `{"environment_id":"environment"}{}`, "Bearer private-test-token", https, nil, 400, 0},
		{"foreign", `{"environment_id":"environment"}`, "Bearer private-test-token", https, sessions.ErrNotFound, 401, 1},
		{"conflict", `{"environment_id":"environment"}`, "Bearer private-test-token", https, sessions.ErrDeviceBindingConflict, 409, 1},
		{"internal failure", `{"environment_id":"environment"}`, "Bearer private-test-token", https, errors.New("private database detail"), 503, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &enrollmentStub{err: test.err}
			req := httptest.NewRequest("POST", "/api/v1/agent-daemon/enroll", strings.NewReader(test.body))
			req.Header.Set("Authorization", test.authorization)
			response := httptest.NewRecorder()
			EnrollmentHandler(s, test.origin).ServeHTTP(response, req)
			if response.Code != test.status || s.calls != test.calls {
				t.Fatalf("status/calls %d/%d", response.Code, s.calls)
			}
			if strings.Contains(response.Body.String(), "private") {
				t.Fatal("enrollment leaked confidential details")
			}
			if response.Code == 200 && (response.Body.String() != `{"link_url":"wss://core.example/api/v1/sandbox-link","resource":{"tenant_id":"tenant","environment_id":"environment","kind":"enrollment","id":"enrollment","generation":2}}`+"\n" || response.Header().Get("Cache-Control") != "no-store") {
				t.Fatalf("enrollment response %s", response.Body.String())
			}
			if test.name == "no wss Link" && !strings.Contains(response.Body.String(), `"code":"no_sandbox_link"`) {
				t.Fatalf("no Link response %s", response.Body.String())
			}
		})
	}
}
