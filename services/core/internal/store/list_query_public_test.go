package store_test

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
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
	s := store.NewWithCredentialCipher(pool, cipher)
	token, foreign := uuid.NewString(), uuid.NewString()
	auth, err := newTestAuthenticator([]testAPIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "query-owner", TokenSHA256: device.HashCredential(token), TenantID: uuid.NewString()},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "query-foreign", TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Use real admission while leaving dispatch paused. Public cancellation retains
	// the queued history; this fixture does not perform native or model execution.
	worker, err := execution.StartWorker(t.Context(), &execution.Dispatcher{Store: s})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopped, cancel := context.WithCancel(context.Background())
		cancel()
		if err := worker.Run(stopped); err != context.Canceled {
			t.Error(err)
		}
	})
	handler, err := api.NewHandler(s, auth, "codex", api.WithExecution(worker), api.WithSkills(s), api.WithSourceFiles(s))
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
