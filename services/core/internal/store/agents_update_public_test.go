package store_test

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestAgentUpdateOfficialClient(t *testing.T) {
	python := os.Getenv("OAC_TEST_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	s, db := newTestStoreDB(t)
	token, foreign := uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential(token), TenantID: uuid.NewString()},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	h, err := publicHandler(t, s, db, auth, "codex", storeExecution(t, s))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	recoveredStore, recoveredDB := store.New(db.pool), fixtureDB{pool: db.pool}
	h, err = publicHandler(t, recoveredStore, recoveredDB, auth, "codex", storeExecution(t, recoveredStore))
	if err != nil {
		t.Fatal(err)
	}
	recovered := httptest.NewServer(h)
	defer recovered.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "../../tests/official_agent_update.py", server.URL, token, foreign, recovered.URL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("official Agent update: %v %s", err, out)
	}
	t.Log(string(out))
}
