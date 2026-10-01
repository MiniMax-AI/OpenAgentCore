package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/credentialcrypto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func TestSandboxDeploymentViewRecordsTemplateBuildAndSuspension(t *testing.T) {
	_, pool := newManagedTestStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := withPlacement(t, NewWithCredentialCipher(pool, cipher))
	w := executionWriter(t, s)
	changes := deploymentExecution(t, w)
	id := uuid.NewString()
	if err := changes.Claim(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	input := e2bSelection()
	input.Resources = sandbox.Resources{}
	var invalid *deployment.ConfigurationError
	if _, err := changes.Initialize(t.Context(), id, input); !errors.As(err, &invalid) {
		t.Fatal("omitted E2B resources were stored without a validated build", err)
	}
	disk := int32(24063)
	input.Resources = sandbox.Resources{CPUs: 2, MemoryMiB: 2048}
	input.Configuration.(*e2b.DeploymentConfiguration).TemplateBuild = &e2b.DeploymentBuild{Status: "ready", CPUs: 2, MemoryMiB: 2048, RootDiskMiB: &disk}
	view, err := changes.Initialize(t.Context(), id, input)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(view)
	for _, want := range []string{
		`"specification":{"resources":{"cpus":2,"memory_mib":2048}}`,
		`"template_build":{"status":"ready","resources":{"cpus":2,"memory_mib":2048,"root_disk_mib":24063}}`,
		`"suspension":{"idle_seconds":300,"retention_seconds":86400}`,
	} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Fatalf("E2B view lacks %s: %s", want, raw)
		}
	}
	// Selections saved before Core recorded the build report it as unknown;
	// saving the identical selection again records it without a new generation.
	forget := func() {
		t.Helper()
		if _, err := pool.Exec(t.Context(), "UPDATE runtime_deployment SET provider_metadata='{}'::jsonb"); err != nil {
			t.Fatal(err)
		}
	}
	recorded := []byte(`"template_build":{"status":"ready","resources":{"cpus":2,"memory_mib":2048,"root_disk_mib":24063}}`)
	forget()
	view, err = deploymentService(t, s).View(t.Context())
	raw, _ = json.Marshal(view)
	if err != nil || !bytes.Contains(raw, []byte(`"metadata":{}`)) {
		t.Fatalf("unknown build was not null: %s %v", raw, err)
	}
	input.ExpectedGeneration = 1
	view, err = changes.Initialize(t.Context(), id, input)
	raw, _ = json.Marshal(view)
	if err != nil || view.Generation != 1 || !bytes.Contains(raw, recorded) {
		t.Fatalf("identical POST did not record the build: %s %v", raw, err)
	}
	forget()
	view, err = changes.Update(SandboxResetTestContext(t.Context()), id, input)
	raw, _ = json.Marshal(view)
	if err != nil || view.Generation != 1 || !bytes.Contains(raw, recorded) {
		t.Fatalf("identical PUT did not record the build: %s %v", raw, err)
	}
	view, err = resetAndSelect(t, w, id, 1, sandbox.Selection{DeploymentSpec: SandboxDeploymentTestSpec("microsandbox"), Provider: "microsandbox"})
	if err != nil || string(view.Configuration) != "{}" || view.Suspension == nil || view.Suspension.IdleSeconds != 300 || view.Suspension.RetentionSeconds != 86400 {
		t.Fatalf("microsandbox suspension view = %+v %v", view, err)
	}
}

func e2bPublicConfiguration(t *testing.T, v deployment.View) *e2b.DeploymentConfiguration {
	t.Helper()
	c, err := (e2b.ConfigurationAdapter{}).Decode(sandbox.ConfigurationRecord{Public: v.Configuration, Metadata: v.Metadata})
	if err != nil {
		t.Fatal(err)
	}
	return c.(*e2b.DeploymentConfiguration)
}
