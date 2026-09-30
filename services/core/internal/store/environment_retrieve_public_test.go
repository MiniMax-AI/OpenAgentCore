package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/identity"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestEnvironmentRetrievalOfficialClient(t *testing.T) {
	python := os.Getenv("OAC_TEST_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	s, db := newModelTestStoreDB(t)
	tenant, foreignTenant := uuid.NewString(), uuid.NewString()
	principal := store.FixtureExecutorPrincipal(t, s, tenant)
	token, peer, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{
		{OrganizationID: principal.OrganizationID, ProjectID: tenant, SubjectKind: principal.SubjectKind, SubjectID: principal.SubjectID, TokenSHA256: runtimedevice.HashCredential(token), TenantID: tenant},
		{OrganizationID: principal.OrganizationID, ProjectID: tenant, SubjectKind: principal.SubjectKind, SubjectID: principal.SubjectID, TokenSHA256: runtimedevice.HashCredential(peer), TenantID: tenant},
		{OrganizationID: principal.OrganizationID, ProjectID: foreignTenant, SubjectKind: principal.SubjectKind, SubjectID: principal.SubjectID, TokenSHA256: runtimedevice.HashCredential(foreign), TenantID: foreignTenant},
	})
	if err := s.EnsureProjectScopes(t.Context(), []identity.ProjectScope{{TenantID: tenant, OrganizationID: principal.OrganizationID, ProjectID: tenant}, {TenantID: foreignTenant, OrganizationID: principal.OrganizationID, ProjectID: foreignTenant}}); err != nil {
		t.Fatal(err)
	}
	executor, err := fixtureSessionService(t, db).IssueExecutorCredential(t.Context(), principal, uuid.NewString(), "")
	if err != nil {
		t.Fatal(err)
	}
	revoked := false
	defer func() {
		if !revoked {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := fixtureSessionService(t, db).RevokeExecutorCredential(ctx, principal, executor.KeyID); err != nil {
				t.Error("owned executor credential cleanup failed", err)
			}
		}
	}()
	handler, err := publicHandler(t, s, db, auth, "codex", storeExecution(t, s), executorURL("https://private-registry.example"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	settings := map[string]string{"base": server.URL, "token": token, "peer_token": peer, "foreign_token": foreign, "executor_token": executor.Token}
	run := func() map[string]string {
		t.Helper()
		input, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, python, "../../tests/official_environment_retrieve.py")
		command.Stdin = bytes.NewReader(input)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("official Environment retrieval: %v %s", err, output)
		}
		var result map[string]string
		if err := json.Unmarshal(output, &result); err != nil {
			t.Fatal("invalid official Environment retrieval result", err)
		}
		return result
	}
	result := run()
	before, err := fixtureSessionStore(db).GetEnvironment(t.Context(), tenant, result["environment_id"])
	if err != nil {
		t.Fatal(err)
	}
	if err := fixtureSessionService(t, db).RevokeExecutorCredential(t.Context(), principal, executor.KeyID); err != nil {
		t.Fatal(err)
	}
	revoked = true
	server.Close()
	db.pool.Close()
	reopened, reopenedDB := newModelTestStoreDB(t)
	handler, err = publicHandler(t, reopened, reopenedDB, auth, "codex")
	if err != nil {
		t.Fatal(err)
	}
	recovered := httptest.NewServer(handler)
	defer recovered.Close()
	settings["base"], settings["phase"] = recovered.URL, "reopened"
	for key, value := range result {
		settings[key] = value
	}
	run()
	after, err := fixtureSessionStore(reopenedDB).GetEnvironment(t.Context(), tenant, before.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("public retrieval changed durable Environment state", err)
	}
	var history int
	if err := reopenedDB.pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM turns WHERE session_id=$1) +
		(SELECT count(*) FROM environment_input_reservations WHERE session_id=$1) +
		(SELECT count(*) FROM session_events WHERE session_id=$1)`, before.SessionID).Scan(&history); err != nil || history != 0 {
		t.Fatal("read-only retrieval admitted work or emitted events", history, err)
	}
}
