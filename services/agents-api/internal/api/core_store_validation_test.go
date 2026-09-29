package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestCoreStoreValidationFieldsAndPublicFallback(t *testing.T) {
	s := &store.Store{}
	_, nameErr := s.CreateProject(context.Background(), managementProjectID, strings.Repeat("private-name", 20))
	upperCapacityErr := s.UpdateRuntimeNode(context.Background(), managementProjectID, store.RuntimeNodeUpdate{Name: "node", MaxActive: 1000001, MaxRetained: 8})
	capacityErr := s.UpdateRuntimeNode(context.Background(), managementProjectID, store.RuntimeNodeUpdate{Name: "node", MaxActive: 0})
	_, resourceErr := store.SandboxSetupForSelection(managementProjectID, store.SandboxDeploymentSetupRequest{Provider: "docker", DeploymentSpec: sandbox.DeploymentSpec{}})
	_, runtimeErr := store.SandboxSetupForSelection(managementProjectID, store.SandboxDeploymentSetupRequest{Provider: "docker", DeploymentSpec: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 1, MemoryMiB: 512}}})
	for _, tc := range []struct {
		err                       error
		code, param               string
		details                   map[string]any
		publicMessage, publicCode string
	}{
		{&sandbox.ValidationError{Param: "resources", Message: "E2B template build resources are outside the supported sandbox limits; select another build"}, "invalid_sandbox_configuration", "resources", nil, "E2B template build resources are outside the supported sandbox limits; select another build", "invalid_sandbox_configuration"},
		{nameErr, "invalid_name", "name", map[string]any{"max_length": float64(128)}, "Invalid resource identifier or request limits.", "invalid_request"},
		{upperCapacityErr, "invalid_node_capacity", "max_active", map[string]any{"min": float64(1), "max": float64(1000000)}, "Invalid resource identifier or request limits.", "invalid_request"},
		{capacityErr, "invalid_node_capacity", "max_active", map[string]any{"min": float64(1), "max": float64(1000000)}, "Invalid resource identifier or request limits.", "invalid_request"},
		{resourceErr, "invalid_sandbox_configuration", "resources.cpus", map[string]any{"min": float64(1), "max": float64(255)}, "invalid sandbox configuration: cpus must be 1..255 and memory_mib must be 512..1048576", "invalid_sandbox_configuration"},
		{runtimeErr, "invalid_sandbox_configuration", "runtime", nil, "invalid sandbox configuration: managed nodes require a pinned Runtime release", "invalid_sandbox_configuration"},
	} {
		if tc.err == nil {
			t.Fatal("missing validator error")
		}
		for _, core := range []bool{false, true} {
			handler := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeStoreError(w, r, fmt.Errorf("wrapped: %w", tc.err)) }))
			if core {
				handler = coreErrorResponses(handler)
			}
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, httptest.NewRequest("POST", "/core/v1/test", nil))
			if out.Code != 400 || strings.Contains(out.Body.String(), "private-name") {
				t.Fatal(out.Code, out.Body)
			}
			if !core {
				golden := `{"error":{"message":"` + tc.publicMessage + `","type":"invalid_request_error","code":"` + tc.publicCode + `","param":null}}` + "\n"
				if out.Body.String() != golden {
					t.Fatal("public/machine fallback changed", out.Body)
				}
				continue
			}
			var body struct {
				Error struct {
					Code, Param string
					Details     map[string]any
				}
			}
			if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != tc.code || body.Error.Param != tc.param || !reflect.DeepEqual(body.Error.Details, tc.details) {
				t.Fatal(out.Body)
			}
		}
	}
}

func TestCoreActiveCapacityUpperBoundNamesSubmittedField(t *testing.T) {
	project, _ := NewAuthenticator([]APIKey{callerBinding()})
	admin, _ := NewDeploymentAuthenticator([]string{device.HashCredential("administrator")})
	h, err := NewHandler(&recordingStore{}, project, "codex", WithSandboxManager(&store.Store{}, admin))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/core/v1/sandbox/enrollment-tokens", `{"max_active":1000001}`},
		{http.MethodPatch, "/core/v1/sandbox/nodes/11111111-1111-4111-8111-111111111111", `{"name":"node","max_active":1000001,"max_retained":8}`},
	} {
		out := projectKeyHTTP(h, tc.method, tc.path, "administrator", tc.body)
		const golden = `{"error":{"message":"Node capacity must be positive, at most 1000000, and max_retained must be at least max_active.","type":"invalid_request_error","code":"invalid_node_capacity","param":"max_active","details":{"max":1000000,"min":1}}}` + "\n"
		if out.Code != http.StatusBadRequest || out.Body.String() != golden {
			t.Errorf("%s: %d %s", tc.method, out.Code, out.Body)
		}
	}
}
