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

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestSkillsOfficialClientPostgres(t *testing.T) {
	python := os.Getenv("OAC_TEST_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	_, pool := store.NewTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{51}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s, db := store.NewWithCredentialCipher(pool, cipher), fixtureDB{pool: pool, cipher: cipher}
	token, foreign := uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential(token), TenantID: uuid.NewString()},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: runtimedevice.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	h, err := publicHandler(t, s, db, auth, "codex")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	recoveredStore, recoveredDB := store.NewWithCredentialCipher(pool, cipher), fixtureDB{pool: pool, cipher: cipher}
	h, err = publicHandler(t, recoveredStore, recoveredDB, auth, "codex")
	if err != nil {
		t.Fatal(err)
	}
	recovered := httptest.NewServer(h)
	defer recovered.Close()
	settings, err := json.Marshal(map[string]string{"base": server.URL, "recovered": recovered.URL, "token": token, "foreign": foreign})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, python, "../../tests/official_skills.py")
	command.Stdin = bytes.NewReader(settings)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Skills official client: %v %s", err, output)
	}
	t.Log(string(output))
}
