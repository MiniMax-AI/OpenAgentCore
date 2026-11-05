package integration

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/google/uuid"
)

func TestAutomationsOfficialClient(t *testing.T) {
	python := os.Getenv("OAC_TEST_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	s, _ := newManagedTestStore(t)
	token, foreign, tenant, project := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{
		{OrganizationID: "test-org", ProjectID: project, SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential(token), TenantID: tenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	// Delivery resolves the owning Project's principal from its rows.
	if _, err := s.pool.Exec(t.Context(), "INSERT INTO execution_project_scopes(tenant_id,organization_id,project_id) VALUES($1,'test-org',$2)", tenant, project); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(t.Context(), "INSERT INTO projects(id,name,tenant_id,subject_kind,subject_id) VALUES($1,'Automation fixture',$1,'service_account','test-runner')", tenant); err != nil {
		t.Fatal(err)
	}
	var deps api.Dependencies
	h, err := publicHandler(t, s, auth, "codex", func(d *api.Dependencies) { deps = *d })
	if err != nil {
		t.Fatal(err)
	}
	_, deliver, err := api.NewHandler(deps)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			if _, failed, err := deliver(ctx); err != nil && ctx.Err() == nil {
				t.Errorf("delivery failed %d: %v", failed, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	out, err := exec.CommandContext(ctx, python, "../../tests/official_automations.py", server.URL, token, foreign).CombinedOutput()
	if err != nil {
		t.Fatalf("official Automations: %v %s", err, out)
	}
	t.Log(string(out))
}
