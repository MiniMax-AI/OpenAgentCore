package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestTemplateCompositionOfficialClientPostgres(t *testing.T) {
	python := os.Getenv("OAC_TEST_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	_, pool := store.NewTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{84}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s, reopenedStore := store.NewWithCredentialCipher(pool, cipher), store.NewWithCredentialCipher(pool, cipher)
	db := fixtureDB{pool: pool, cipher: cipher} // built both Stores
	tenant, foreignTenant, token, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth := newTestAuthenticator(t, []testAPIKey{
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "composition-owner", TokenSHA256: runtimedevice.HashCredential(token), TenantID: tenant},
		{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "composition-foreign", TokenSHA256: runtimedevice.HashCredential(foreign), TenantID: foreignTenant},
	})
	serve := func(current *store.Store) *httptest.Server {
		t.Helper()
		// Hosted admission and freezing use the real Store; no Runtime or model runs.
		h, err := publicHandler(t, current, db, auth, "codex", storeExecution(t, current), managedSandboxes(t, current, db), fixtureDeploymentProvider())
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(h)
		t.Cleanup(server.Close)
		return server
	}
	server, reopened := serve(s), serve(reopenedStore)
	marker := "private-composition-" + uuid.NewString()
	settings, err := json.Marshal(map[string]string{"base": server.URL, "recovered": reopened.URL, "token": token, "foreign": foreign, "canary": marker})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, python, "../../tests/official_template_composition.py")
	command.Stdin = bytes.NewReader(settings)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("template composition official client: %v %s", err, output)
	}
	var receipt struct {
		Sessions     map[string]string `json:"sessions"`
		RejectedKeys []string          `json:"rejected_keys"`
	}
	if err := json.Unmarshal(output, &receipt); err != nil {
		t.Fatalf("invalid acceptance receipt: %v %s", err, output)
	}
	if len(receipt.Sessions) != 6 || len(receipt.RejectedKeys) != 10 {
		t.Fatalf("incomplete acceptance receipt: %s", output)
	}
	t.Cleanup(func() {
		for _, id := range receipt.Sessions {
			if err := s.DeleteSession(context.Background(), tenant, id); err != nil {
				t.Error(err)
			}
		}
	})
	// A second handler/Store exercises reopened persistence, not an OS process restart.
	for label, id := range receipt.Sessions {
		env := map[string]string{"TEMPLATE_ONLY": marker + "-template-env", "SHARED": marker + "-template-shared"}
		commands := []environmentconfig.SetupCommand{{Command: "printf " + marker + "-template-command", CWD: "/workspace"}}
		packages := v1.EnvironmentPackages{Python: []string{"packaging==25.0"}, NPM: []string{"semver@7.7.2"}}
		paths := []string{"/workspace/template-only.txt", "/workspace/overlap.txt"}
		contents := []string{marker + "-template-file", marker + "-source"}
		switch label {
		case "populated":
			env["SHARED"], env["INLINE_ONLY"] = marker+"-inline-shared", marker+"-inline-env"
			commands = []environmentconfig.SetupCommand{{Command: "printf " + marker + "-inline-one"}, {Command: "printf " + marker + "-inline-two", CWD: "/workspace"}}
			packages.Python, packages.NPM = []string{"idna==3.10"}, []string{}
			paths = []string{"/workspace/overlap.txt", "/workspace/selected-source.txt"}
			contents = []string{marker + "-inline-file", marker + "-source"}
		case "empty":
			commands = nil
			paths, contents = nil, nil
		case "excluded-source":
			paths, contents = nil, nil
		case "omitted", "null", "nested-null":
		default:
			t.Fatalf("unknown case %q", label)
		}
		// A reader built after the requests reads the frozen setup and files.
		current := fixtureSessionStore(db)
		setup, err := current.ReadEnvironmentSetup(t.Context(), tenant, id)
		if err != nil || !reflect.DeepEqual(setup.Env, env) || !reflect.DeepEqual(setup.PackageMetadata(), packages) || len(setup.Commands) != len(commands) {
			t.Fatalf("%s durable setup differs: %v", label, err)
		}
		for i, want := range commands {
			if setup.Commands[i] != want {
				t.Fatalf("%s command order differs", label)
			}
		}
		if _, err := current.ReadEnvironmentSetup(t.Context(), foreignTenant, id); !errors.Is(err, sessions.ErrNotFound) {
			t.Fatalf("%s foreign setup read: %v", label, err)
		}
		for position, want := range contents {
			metadata, body, err := current.ReadInitialEnvironmentFile(t.Context(), tenant, id, position)
			if err != nil || string(body) != want || metadata.Path != paths[position] || metadata.SizeBytes == nil || *metadata.SizeBytes != int64(len(want)) {
				t.Fatalf("%s frozen file %d differs: %v", label, position, err)
			}
			if _, _, err := current.ReadInitialEnvironmentFile(t.Context(), foreignTenant, id, position); err == nil {
				t.Fatalf("%s foreign file read succeeded", label)
			}
			var encrypted []byte
			if err := pool.QueryRow(t.Context(), "SELECT contents FROM initial_environment_files WHERE id=$1", metadata.ID).Scan(&encrypted); err != nil || bytes.Contains(encrypted, []byte(marker)) {
				t.Fatalf("%s plaintext file storage: %v", label, err)
			}
		}
		var encrypted, configuration []byte
		if err := pool.QueryRow(t.Context(), "SELECT e.contents,s.configuration FROM environment_setups e JOIN sessions s ON s.id=e.session_id WHERE s.id=$1", id).Scan(&encrypted, &configuration); err != nil || bytes.Contains(encrypted, []byte(marker)) || bytes.Contains(configuration, []byte(marker)) {
			t.Fatalf("%s plaintext setup storage: %v", label, err)
		}
	}
	// Fresh tenant counts include deleted rows too, so rejected transactional creates
	// cannot hide partial Sessions, Environments or snapshots behind soft deletion.
	checks := []struct {
		query string
		want  int
	}{
		{"SELECT count(*) FROM sessions WHERE tenant_id=$1", 6},
		{"SELECT count(*) FROM environments e JOIN sessions s ON s.id=e.session_id WHERE s.tenant_id=$1", 6},
		{"SELECT count(*) FROM environment_setups e JOIN sessions s ON s.id=e.session_id WHERE s.tenant_id=$1", 6},
		{"SELECT count(*) FROM initial_environment_files f JOIN sessions s ON s.id=f.session_id WHERE s.tenant_id=$1", 8},
		{"SELECT count(*) FROM turns t JOIN sessions s ON s.id=t.session_id WHERE s.tenant_id=$1", 0},
		{"SELECT count(*) FROM environment_input_reservations e JOIN sessions s ON s.id=e.session_id WHERE s.tenant_id=$1", 0},
	}
	for _, check := range checks {
		var count int
		if err := pool.QueryRow(t.Context(), check.query, tenant).Scan(&count); err != nil || count != check.want {
			t.Fatalf("admission residue: got %d want %d: %v", count, check.want, err)
		}
	}
	for _, owner := range []string{tenant, foreignTenant} {
		var rejected int
		if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM sessions WHERE tenant_id=$1 AND idempotency_key=ANY($2::text[])", owner, receipt.RejectedKeys).Scan(&rejected); err != nil || rejected != 0 {
			t.Fatalf("rejected creation persisted: %d %v", rejected, err)
		}
	}
	t.Log("six composition cases passed fixed SDK/raw HTTP, reopened encrypted snapshots, isolation, rejection rollback and frozen retries; no native/model execution")
}
