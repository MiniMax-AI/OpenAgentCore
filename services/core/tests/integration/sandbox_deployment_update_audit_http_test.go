package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

// Keep credential verification at the controlled provider boundary; the HTTP
// handler, execution owner, deployment transaction and audit store are real.
type deploymentAuditProviders struct{ fixtureProviderRegistry }

func (*deploymentAuditProviders) VerifyCredential(context.Context, sandbox.DirectConfig, []sandbox.Reference) error {
	return nil
}

func TestSandboxDeploymentUpdateAuditHTTPPostgres(t *testing.T) {
	s, pool := newManagedTestStore(t)
	installation := uuid.NewString()
	provider := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	dispatcher := webDispatcher(t, installation, provider, runtimegateway.NewRegistry(), nil)
	dispatcher.Providers = &deploymentAuditProviders{*dispatcher.Providers.(*fixtureProviderRegistry)}
	worker := startWorker(t, t.Context(), s, dispatcher)
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = worker.Run(ctx)
	})
	if _, err := worker.InitializeSandboxDeployment(t.Context(), e2bSelection()); err != nil {
		t.Fatal(err)
	}
	handler, err := publicHandler(t, s, nil, "codex", func(d *api.Dependencies) {
		d.Sandboxes.DeploymentChanges = worker
	})
	if err != nil {
		t.Fatal(err)
	}
	request := func(token, actor, template string, generation uint64) *httptest.ResponseRecorder {
		t.Helper()
		body := fmt.Sprintf(`{"provider":"e2b","expected_generation":%d,"resources":{"cpus":2,"memory_mib":2048},"configuration":{"template":%q}}`, generation, template)
		r := httptest.NewRequest(http.MethodPut, "/core/v1/sandbox/deployment", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Core-Console-Actor", actor)
		r.Header.Set("X-Request-Id", "untrusted-request-id")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	template := "updated:" + uuid.NewString()
	updated := request("admin", "deployment operator", template, 1)
	var view deployment.View
	if updated.Code != http.StatusOK || json.Unmarshal(updated.Body.Bytes(), &view) != nil || view.Generation != 2 {
		t.Fatalf("update: %d %s", updated.Code, updated.Body)
	}
	stored, err := deploymentService(t, s).View(t.Context())
	if err != nil || stored.Generation != view.Generation || !strings.Contains(string(stored.Configuration), `"template":"`+template+`"`) {
		t.Fatalf("committed deployment: %+v, %v", stored, err)
	}
	var credential, actor, requestID, traceID, resourceID string
	var deploymentWide bool
	if err := pool.QueryRow(t.Context(), `SELECT admin_credential_id,actor_label,request_id,trace_id,resource_id,tenant_id IS NULL AND project_id IS NULL
 FROM admin_audit_log WHERE action='change' AND resource_type='sandbox_deployment'`).Scan(&credential, &actor, &requestID, &traceID, &resourceID, &deploymentWide); err != nil {
		t.Fatal(err)
	}
	traceParts := strings.Split(updated.Header().Get("Traceparent"), "-")
	if credential != runtimedevice.HashCredential("admin")[:8] || actor != "deployment operator" || requestID == "" || requestID != updated.Header().Get("X-Request-Id") || requestID == "untrusted-request-id" || len(traceParts) != 4 || traceID != traceParts[1] || resourceID != installation || !deploymentWide {
		t.Fatal("deployment audit lost authenticated identity or request correlation")
	}
	tables := []string{"runtime_deployment", "runtime_deployment_generations", "admin_audit_log"}
	before := adminMutationSnapshot(t, s, tables...)
	for _, tc := range []struct {
		name, token, actor string
		generation         uint64
		status             int
	}{
		{"unauthorized", "caller", "deployment operator", 2, http.StatusUnauthorized},
		{"stale", "admin", "deployment operator", 1, http.StatusConflict},
		{"invalid audit source", "admin", strings.Repeat("x", 129), 2, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := request(tc.token, tc.actor, "rejected:"+uuid.NewString(), tc.generation)
			if w.Code != tc.status {
				t.Fatalf("update: %d %s", w.Code, w.Body)
			}
			if !reflect.DeepEqual(before, adminMutationSnapshot(t, s, tables...)) {
				t.Fatal("rejected update changed the deployment or its audit history")
			}
		})
	}
}
