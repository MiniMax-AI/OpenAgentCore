package store_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestListQueryOfficialClientPostgres(t *testing.T) {
	python := os.Getenv("OAC_TEST_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	_, pool := store.NewTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{72}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s, db := store.NewWithCredentialCipher(pool, cipher), fixtureDB{pool: pool, cipher: cipher}
	token, foreign := uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "query-owner", TokenSHA256: runtimedevice.HashCredential(token), TenantID: uuid.NewString()},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "query-foreign", TokenSHA256: runtimedevice.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	// Use real admission while leaving dispatch paused. Public cancellation retains
	// the queued history; this fixture does not perform native or model execution.
	worker := startWorker(t, t.Context(), db, &execution.Dispatcher{Store: s})
	t.Cleanup(func() {
		stopped, cancel := context.WithCancel(context.Background())
		cancel()
		if err := worker.Run(stopped); err != context.Canceled {
			t.Error(err)
		}
	})
	handler, err := publicHandler(t, s, db, auth, "codex", workerExecution(worker))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, python, "../../tests/official_list_query.py", server.URL, token, foreign)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("official list query acceptance: %v %s", err, output)
	}
	t.Log(string(output))
}
