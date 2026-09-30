package store_test

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/google/uuid"
)

func TestSourceFileErrorsOfficialClientPostgres(t *testing.T) {
	python := os.Getenv("OAC_TEST_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	s, db := newTestStoreDB(t)
	token, foreign := uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "files-owner", TokenSHA256: runtimedevice.HashCredential(token), TenantID: uuid.NewString()},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "files-foreign", TokenSHA256: runtimedevice.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	h, err := publicHandler(t, s, db, auth, "codex")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, python, "../../tests/official_source_file_errors.py", server.URL, token, foreign).CombinedOutput()
	if err != nil {
		t.Fatalf("official Files errors acceptance: %v %s", err, output)
	}
	t.Log(string(output))
}
