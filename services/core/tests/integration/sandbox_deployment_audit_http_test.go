package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// The HTTP boundary must supply provenance; this bridge passes its untouched
// context into the actual deployment transaction, including its audit write.
type auditedDeploymentChanges struct {
	strictStandIn
	operations   *deployment.ExecutionOperations
	installation string
}

func (d auditedDeploymentChanges) UpdateSandboxDeployment(ctx context.Context, input sandbox.Selection) (deployment.View, error) {
	return d.operations.Update(ctx, d.installation, input)
}

func TestSandboxDeploymentUpdateHTTPCommitsWithAdministratorAudit(t *testing.T) {
	store, writer, initial, input := webSpecificationFixture(t, "docker")
	changes := auditedDeploymentChanges{strictStandIn{t}, deploymentExecution(t, writer), initial.InstallationID}
	handler, err := publicHandler(t, store, newTestAuthenticator(t, nil), "codex", func(d *api.Dependencies) { d.Sandboxes.DeploymentChanges = changes })
	if err != nil {
		t.Fatal(err)
	}
	call := func(generation uint64, cpus uint32, key, actor string, status int) *httptest.ResponseRecorder {
		t.Helper()
		resources := input.Resources
		resources.CPUs = cpus
		body, err := json.Marshal(map[string]any{"expected_generation": generation, "provider": "docker", "configuration": map[string]any{}, "resources": resources, "runtime": input.Runtime})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPut, "/core/v1/sandbox/deployment", strings.NewReader(string(body)))
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Core-Console-Actor", actor)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != status {
			t.Fatalf("update status=%d want=%d body=%s", response.Code, status, response.Body)
		}
		return response
	}
	call(1, 3, "invalid", "operator", http.StatusUnauthorized)
	response := call(1, 3, "admin", "operator", http.StatusOK)
	current, err := deploymentService(t, store).View(t.Context())
	if err != nil || current.Generation != 2 || current.Specification.Resources.CPUs != 3 {
		t.Fatal("deployment did not commit", current, err)
	}
	var credential, actor, requestID, traceID, action, kind, resource string
	var project, tenant *string
	err = store.pool.QueryRow(t.Context(), `SELECT admin_credential_id,actor_label,request_id,trace_id,action,resource_type,resource_id,project_id,tenant_id FROM admin_audit_log WHERE resource_type='sandbox_deployment' AND resource_id=$1`, initial.InstallationID).Scan(&credential, &actor, &requestID, &traceID, &action, &kind, &resource, &project, &tenant)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("admin"))
	if credential != hex.EncodeToString(digest[:])[:8] || actor != "operator" || requestID == "" || requestID != response.Header().Get("x-request-id") || traceID == "" || action != "change" || kind != "sandbox_deployment" || resource != initial.InstallationID || project != nil || tenant != nil {
		t.Fatal("administrator provenance differs", credential, actor, requestID, traceID, project, tenant)
	}
	// Invalid audit provenance still rolls the mutation back atomically.
	call(2, 4, "admin", strings.Repeat("a", 129), http.StatusBadRequest)
	call(1, 4, "admin", "operator", http.StatusConflict)
	current, err = deploymentService(t, store).View(t.Context())
	if err != nil || current.Generation != 2 || current.Specification.Resources.CPUs != 3 {
		t.Fatal("rejected update changed deployment", current, err)
	}
	var count int
	if err = store.pool.QueryRow(t.Context(), `SELECT count(*) FROM admin_audit_log WHERE resource_type='sandbox_deployment' AND resource_id=$1`, initial.InstallationID).Scan(&count); err != nil || count != 1 {
		t.Fatal("rejected request wrote audit", count, err)
	}
}
