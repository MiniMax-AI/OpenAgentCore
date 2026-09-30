package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/device"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestTemplateNullSelectionOfficialClientPostgres(t *testing.T) {
	python := os.Getenv("OAC_TEST_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	_, pool := store.NewTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{87}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := store.NewWithCredentialCipher(pool, cipher)
	reopenedStore := store.NewWithCredentialCipher(pool, cipher)
	tenant, foreignTenant, token, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth, err := newTestAuthenticator([]testAPIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "selection-owner", TokenSHA256: device.HashCredential(token), TenantID: tenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "selection-foreign", TokenSHA256: device.HashCredential(foreign), TenantID: foreignTenant},
	})
	if err != nil {
		t.Fatal(err)
	}
	serve := func(current *store.Store) *httptest.Server {
		t.Helper()
		h, err := api.NewHandler(current, auth, "codex", api.WithHostedEnvironments(), api.WithExecution(current), api.WithSourceFiles(current), api.WithSkills(current), fixtureDeploymentProvider())
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(h)
		t.Cleanup(server.Close)
		return server
	}
	server, reopened := serve(s), serve(reopenedStore)
	marker := "private-selection-" + uuid.NewString()
	settings, err := json.Marshal(map[string]string{"base": server.URL, "recovered": reopened.URL, "token": token, "foreign": foreign, "canary": marker})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, python, "../../tests/official_template_null_selection.py")
	command.Stdin = bytes.NewReader(settings)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("template null selection official client: %v %s", err, output)
	}
	var receipt struct {
		Sessions     map[string]string `json:"sessions"`
		RejectedKeys []string          `json:"rejected_keys"`
		Expected     map[string]struct {
			Skills                []store.EnvironmentSkillMetadata `json:"skills"`
			Plugins               []agentplugin.Metadata           `json:"plugins"`
			CapabilityDirectories []string                         `json:"capability_directories"`
			SkillDigests          []string                         `json:"skill_digests"`
			PluginDigests         []string                         `json:"plugin_digests"`
		} `json:"expected"`
	}
	if err := json.Unmarshal(output, &receipt); err != nil {
		t.Fatalf("invalid acceptance receipt: %v %s", err, output)
	}
	t.Cleanup(func() {
		for _, id := range receipt.Sessions {
			if err := s.DeleteSession(context.Background(), tenant, id); err != nil {
				t.Error(err)
			}
		}
	})
	if len(receipt.Sessions) != 9 || len(receipt.Expected) != 9 || len(receipt.RejectedKeys) != 10 {
		t.Fatalf("incomplete acceptance receipt: sessions=%d expectations=%d rejections=%d", len(receipt.Sessions), len(receipt.Expected), len(receipt.RejectedKeys))
	}
	for label, id := range receipt.Sessions {
		want, ok := receipt.Expected[label]
		if !ok {
			t.Fatalf("missing expectation for %s", label)
		}
		for _, current := range []*store.Store{s, reopenedStore} {
			setup, err := current.ReadEnvironmentSetup(t.Context(), tenant, id)
			if err != nil {
				t.Fatalf("%s frozen setup: %v", label, err)
			}
			if !reflect.DeepEqual(setup.Env, map[string]string{"PRIVATE_SELECTION": marker}) ||
				!reflect.DeepEqual(setup.SkillMetadata(), want.Skills) || !reflect.DeepEqual(setup.PluginMetadata(), want.Plugins) ||
				!reflect.DeepEqual(append([]string{}, setup.CapabilityDirectories...), want.CapabilityDirectories) {
				t.Fatalf("%s frozen selection changed", label)
			}
			if len(setup.Skills) != len(want.SkillDigests) || len(setup.Plugins) != len(want.PluginDigests) {
				t.Fatalf("%s frozen archive count differs", label)
			}
			for i, skill := range setup.Skills {
				digest := sha256.Sum256(skill.Archive)
				if hex.EncodeToString(digest[:]) != want.SkillDigests[i] {
					t.Fatalf("%s frozen Skill bytes changed", label)
				}
			}
			for i, plugin := range setup.Plugins {
				digest := sha256.Sum256(plugin.Archive)
				if hex.EncodeToString(digest[:]) != want.PluginDigests[i] {
					t.Fatalf("%s frozen Plugin bytes changed", label)
				}
			}
			if _, err := current.ReadEnvironmentSetup(t.Context(), foreignTenant, id); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("%s foreign setup read: %v", label, err)
			}
			file, body, err := current.ReadInitialEnvironmentFile(t.Context(), tenant, id, 0)
			if err != nil || string(body) != marker+"-source" || file.Path != "/workspace/source.txt" {
				t.Fatalf("%s frozen source file changed: %v", label, err)
			}
			if _, _, err := current.ReadInitialEnvironmentFile(t.Context(), foreignTenant, id, 0); err == nil {
				t.Fatalf("%s foreign initial file read: %v", label, err)
			}
		}
		var encryptedSetup, encryptedFile, configuration []byte
		err := pool.QueryRow(t.Context(), "SELECT e.contents,f.contents,s.configuration FROM environment_setups e JOIN sessions s ON s.id=e.session_id JOIN initial_environment_files f ON f.session_id=s.id WHERE s.id=$1", id).Scan(&encryptedSetup, &encryptedFile, &configuration)
		if err != nil || len(encryptedSetup) == 0 || len(encryptedFile) == 0 ||
			bytes.Contains(encryptedSetup, []byte(marker)) || bytes.Contains(encryptedFile, []byte(marker)) || bytes.Contains(configuration, []byte(marker)) {
			t.Fatalf("%s confidential initialization storage: %v", label, err)
		}
	}
	// Counts include soft-deleted rows, so failed admission cannot hide partial state.
	for _, check := range []struct {
		query string
		want  int
	}{
		{"SELECT count(*) FROM sessions WHERE tenant_id=$1", 9},
		{"SELECT count(*) FROM environments e JOIN sessions s ON s.id=e.session_id WHERE s.tenant_id=$1", 9},
		{"SELECT count(*) FROM environment_setups e JOIN sessions s ON s.id=e.session_id WHERE s.tenant_id=$1", 9},
		{"SELECT count(*) FROM initial_environment_files f JOIN sessions s ON s.id=f.session_id WHERE s.tenant_id=$1", 9},
		{"SELECT count(*) FROM turns t JOIN sessions s ON s.id=t.session_id WHERE s.tenant_id=$1", 0},
		{"SELECT count(*) FROM environment_input_reservations e JOIN sessions s ON s.id=e.session_id WHERE s.tenant_id=$1", 0},
	} {
		var count int
		if err := pool.QueryRow(t.Context(), check.query, tenant).Scan(&count); err != nil || count != check.want {
			t.Fatalf("admission residue: got %d want %d: %v", count, check.want, err)
		}
	}
	for _, owner := range []string{tenant, foreignTenant} {
		var count int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE tenant_id=$1 AND idempotency_key=ANY($2::text[])", owner, receipt.RejectedKeys).Scan(&count); err != nil || count != 0 {
			t.Fatalf("rejected creation persisted: %d %v", count, err)
		}
	}
	t.Log("nine supported selection cases and rejected network restrictions passed SDK/raw HTTP, frozen encrypted bytes, tenant isolation, atomic rejection and reopened retries; no Runtime or model execution")
}
