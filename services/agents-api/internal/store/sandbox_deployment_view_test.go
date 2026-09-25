package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
)

func TestSandboxDeploymentViewRecordsTemplateBuildAndSuspension(t *testing.T) {
	_, pool := newManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	w := executionLease(t, s).Store()
	id := uuid.NewString()
	if err := w.ClaimWebSandboxDeployment(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	input := e2bSelection()
	input.Resources = sandbox.Resources{}
	var invalid *SandboxConfigurationError
	if _, err := w.InitializeSandboxDeployment(t.Context(), id, input); !errors.As(err, &invalid) {
		t.Fatal("omitted E2B resources were stored without a validated build", err)
	}
	disk := int32(24063)
	input.Resources = sandbox.Resources{CPUs: 2, MemoryMiB: 2048}
	input.E2B.TemplateBuild = &SandboxE2BTemplateBuild{Status: "ready", CPUs: 2, MemoryMiB: 2048, RootDiskMiB: &disk}
	view, err := w.InitializeSandboxDeployment(t.Context(), id, input)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(view)
	for _, want := range []string{
		`"specification":{"resources":{"cpus":2,"memory_mib":2048}}`,
		`"template_build":{"status":"ready","resources":{"cpus":2,"memory_mib":2048,"root_disk_mib":24063}}`,
		`"suspension":null`,
	} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Fatalf("E2B view lacks %s: %s", want, raw)
		}
	}
	// Selections saved before Core recorded the build report it as unknown;
	// saving the identical selection again records it without a new generation.
	forget := func() {
		t.Helper()
		if _, err := pool.Exec(t.Context(), "UPDATE runtime_deployment SET e2b_template_build_status=NULL, e2b_template_cpus=NULL, e2b_template_memory_mib=NULL, e2b_template_root_disk_mib=NULL"); err != nil {
			t.Fatal(err)
		}
	}
	recorded := []byte(`"template_build":{"status":"ready","resources":{"cpus":2,"memory_mib":2048,"root_disk_mib":24063}}`)
	forget()
	view, err = s.GetRuntimeDeployment(t.Context())
	raw, _ = json.Marshal(view)
	if err != nil || !bytes.Contains(raw, []byte(`"template_build":{"status":null,"resources":{"cpus":null,"memory_mib":null,"root_disk_mib":null}}`)) {
		t.Fatalf("unknown build was not null: %s %v", raw, err)
	}
	view, err = w.InitializeSandboxDeployment(t.Context(), id, input)
	raw, _ = json.Marshal(view)
	if err != nil || view.Generation != 1 || !bytes.Contains(raw, recorded) {
		t.Fatalf("identical POST did not record the build: %s %v", raw, err)
	}
	if _, err := w.SetSandboxMaintenance(t.Context(), id, SandboxMaintenanceRequest{Maintenance: true, ExpectedGeneration: 1}); err != nil {
		t.Fatal(err)
	}
	forget()
	view, err = w.UpdateSandboxDeployment(t.Context(), id, SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: input, ExpectedGeneration: 1})
	raw, _ = json.Marshal(view)
	if err != nil || view.Generation != 1 || !bytes.Contains(raw, recorded) {
		t.Fatalf("identical PUT did not record the build: %s %v", raw, err)
	}
	update := SandboxDeploymentUpdateRequest{SandboxDeploymentSetupRequest: SandboxDeploymentSetupRequest{
		DeploymentSpec: SandboxDeploymentTestSpec("microsandbox"), Provider: "microsandbox"}, ExpectedGeneration: 1}
	view, err = w.UpdateSandboxDeployment(t.Context(), id, update)
	if err != nil || view.E2B != nil || view.Suspension == nil || view.Suspension.IdleSeconds != 300 || view.Suspension.RetentionSeconds != 86400 {
		t.Fatalf("microsandbox suspension view = %+v %v", view, err)
	}
}
