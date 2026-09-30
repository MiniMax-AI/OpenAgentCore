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

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/deploymentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

func TestCoreStoreValidationFieldsAndPublicFallback(t *testing.T) {
	// The deployment validates these inputs before it reaches storage, so
	// storage without a database is enough.
	nodes, err := deployment.NewService(deploymentpg.New(nil, nil), deploymentpg.New(nil, nil), providers.Builtin(), "")
	if err != nil {
		t.Fatal(err)
	}
	upperCapacityErr := nodes.UpdateNode(context.Background(), managementProjectID, deployment.NodeUpdate{Name: "node", MaxActive: 1000001, MaxRetained: 8})
	capacityErr := nodes.UpdateNode(context.Background(), managementProjectID, deployment.NodeUpdate{Name: "node", MaxActive: 0})
	_, resourceErr := nodes.SetupForSelection(managementProjectID, sandbox.Selection{Provider: "docker", DeploymentSpec: sandbox.DeploymentSpec{}})
	_, runtimeErr := nodes.SetupForSelection(managementProjectID, sandbox.Selection{Provider: "docker", DeploymentSpec: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 1, MemoryMiB: 512}}})
	for _, tc := range []struct {
		err                       error
		code, param               string
		details                   map[string]any
		publicMessage, publicCode string
		write                     func(http.ResponseWriter, *http.Request, error)
	}{
		{&sandbox.ValidationError{Param: "resources", Message: "E2B template build resources are outside the supported sandbox limits; select another build"}, "invalid_sandbox_configuration", "resources", nil, "E2B template build resources are outside the supported sandbox limits; select another build", "invalid_sandbox_configuration", nil},
		{&projects.NameError{MaxLength: projects.ProjectNameMaxLength}, "invalid_name", "name", map[string]any{"max_length": float64(128)}, "Invalid resource identifier or request limits.", "invalid_request", writeProjectsError},
		{upperCapacityErr, "invalid_node_capacity", "max_active", map[string]any{"min": float64(1), "max": float64(1000000)}, "Invalid resource identifier or request limits.", "invalid_request", writeDeploymentError},
		{capacityErr, "invalid_node_capacity", "max_active", map[string]any{"min": float64(1), "max": float64(1000000)}, "Invalid resource identifier or request limits.", "invalid_request", writeDeploymentError},
		{resourceErr, "invalid_sandbox_configuration", "resources.cpus", map[string]any{"min": float64(1), "max": float64(255)}, "invalid sandbox configuration: cpus must be 1..255 and memory_mib must be 512..1048576", "invalid_sandbox_configuration", writeDeploymentError},
		{runtimeErr, "invalid_sandbox_configuration", "runtime", nil, "invalid sandbox configuration: managed nodes require a pinned Runtime release", "invalid_sandbox_configuration", writeDeploymentError},
	} {
		if tc.err == nil {
			t.Fatal("missing validator error")
		}
		write := tc.write
		if write == nil {
			write = func(w http.ResponseWriter, r *http.Request, err error) { writeStoreError(w, r, err) }
		}
		for _, core := range []bool{false, true} {
			handler := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { write(w, r, fmt.Errorf("wrapped: %w", tc.err)) }))
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
	deps, fakes := sandboxFakes(t)
	fakes.deployment.createEnrollment = func(_ context.Context, capacity deployment.Capacity) (deployment.EnrollmentToken, error) {
		return deployment.EnrollmentToken{}, nodeCapacity(capacity.MaxActive)
	}
	fakes.deployment.updateNode = func(_ context.Context, _ string, input deployment.NodeUpdate) error {
		return nodeCapacity(input.MaxActive)
	}
	h := newTestHandler(t, deps)
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
