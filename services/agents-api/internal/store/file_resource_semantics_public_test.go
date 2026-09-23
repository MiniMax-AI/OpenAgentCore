package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestFileResourceSemanticsOfficialClientPostgres(t *testing.T) {
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	_, pool := store.NewTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{53}, 32))
	if err != nil {
		t.Fatal(err)
	}
	token, foreign := uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "resources-owner", TokenSHA256: device.HashCredential(token), TenantID: uuid.NewString()},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "resources-foreign", TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	newServer := func() *httptest.Server {
		t.Helper()
		s := store.NewWithCredentialCipher(pool, cipher)
		h, err := api.NewHandler(s, auth, "codex", api.WithSourceFiles(s), api.WithSkills(s))
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(h)
		t.Cleanup(server.Close)
		return server
	}
	server, recovered := newServer(), newServer()
	settings, err := json.Marshal(map[string]string{"base": server.URL, "recovered": recovered.URL, "token": token, "foreign": foreign})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, python, "../../tests/official_file_resource_semantics.py")
	command.Stdin = bytes.NewReader(settings)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("official Files and Skills resource semantics: %v %s", err, output)
	}
	t.Log(string(output))
}
