package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/nativeinstaller"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
)

func installationCatalog(t *testing.T) *nativeinstaller.Catalog {
	t.Helper()
	root := t.TempDir()
	revision := strings.Repeat("a", 40)
	manifest := map[string]any{"source_commit": revision, "platform": "linux/amd64", "artifact_base_url": "https://downloads.example/v1",
		"images": map[string]string{"runtime": "sha256:" + strings.Repeat("1", 64)}, "image_manifest_digests": map[string]string{"runtime": "sha256:" + strings.Repeat("2", 64)},
		"runtime_ref": "oac-runtime@sha256:" + strings.Repeat("3", 64), "microsandbox": map[string]string{"runtime_sha256": strings.Repeat("4", 64), "firmware_sha256": strings.Repeat("5", 64)}}
	requirements, err := providers.Builtin().ArtifactCatalog()
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]any{}
	for _, artifacts := range requirements {
		for _, artifact := range artifacts {
			entries[artifact.Path] = map[string]any{"filename": revision + "-" + artifact.Suffix, "size": 8, "sha256": strings.Repeat("1", 64)}
		}
	}
	manifest["artifacts"] = entries
	raw, _ := json.Marshal(manifest)
	files := map[string][]byte{"manifest.json": raw, "node-install.pyz": []byte("installer"), "runtime/seccomp.json": []byte("{}")}
	sums := ""
	for name, data := range files {
		sums += fmt.Sprintf("%x  %s\n", sha256.Sum256(data), name)
	}
	files["SHA256SUMS"] = []byte(sums)
	for name, data := range files {
		path := filepath.Join(root, "node-payload", "releases", revision, name)
		if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.WriteFile(path, data, 0600) != nil {
			t.Fatal("fixture publication")
		}
	}
	if err := os.WriteFile(filepath.Join(root, "node-payload", "active.json"), []byte(`{"source_commit":"`+revision+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := nativeinstaller.Load(root, revision, providers.Builtin())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { catalog.Close() })
	return catalog
}

func TestInstallationDistributionOwnsDeploymentReleaseAndAnonymousDownloads(t *testing.T) {
	deps, fakes := sandboxFakes(t)
	deps.Distribution = installationCatalog(t)
	source := strings.Repeat("a", 40)
	deps.Installation.SourceCommit = &source
	fakes.installationBindings.addressBindings = func(context.Context) (deployment.AddressBindings, error) { return deployment.AddressBindings{}, nil }
	fakes.deployment.decodeConfiguration = providers.Builtin().DecodeInput
	calls := 0
	selectRelease := func(_ context.Context, input sandbox.Selection) (deployment.View, error) {
		calls++
		if input.Provider == "e2b" {
			if input.Runtime != nil {
				t.Fatal("E2B acquired a node release")
			}
		} else if !reflect.DeepEqual(input.Runtime, deps.Distribution.RuntimeRelease(input.Provider)) {
			t.Fatal("not the installation's release", input.Runtime)
		}
		return deployment.View{Provider: input.Provider, Specification: &input.DeploymentSpec}, nil
	}
	fakes.deploymentChanges.initializeSandboxDeployment = selectRelease
	fakes.deploymentChanges.updateSandboxDeployment = selectRelease
	h := newTestHandler(t, deps)
	for _, method := range []string{"POST", "PUT"} {
		req := httptest.NewRequest(method, "/core/v1/sandbox/deployment", strings.NewReader(`{"provider":"docker","expected_generation":0,"resources":{"cpus":2,"memory_mib":2048}}`))
		req.Header.Set("Authorization", "Bearer administrator")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"image_id":"sha256:`) {
			t.Fatal(method, w.Code, w.Body.String())
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	request := httptest.NewRequest("GET", "/core/v1/installation", nil)
	request.Header.Set("Authorization", "Bearer administrator")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"node_installation":{"installer_sha256":`) || !strings.Contains(w.Body.String(), `"runtime_releases":{"docker":`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, method := range []string{"GET", "HEAD"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "/api/v1/sandbox-node/install/releases/"+source+"/node-install.pyz", nil))
		if w.Code != 200 || method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal(method, w.Code, w.Body.String())
		}
	}
	// A node catalog alone must not enable native installation grants or downloads.
	if deps.Distribution.NativeAvailable() {
		t.Fatal("node catalog implies native artifacts")
	}
}

func TestMissingInstallationReleaseReachesDeploymentValidation(t *testing.T) {
	deps, fakes := sandboxFakes(t)
	fakes.deployment.decodeConfiguration = providers.Builtin().DecodeInput
	fakes.deploymentChanges.initializeSandboxDeployment = func(_ context.Context, input sandbox.Selection) (deployment.View, error) {
		if input.Runtime != nil {
			t.Fatal("missing catalog supplied runtime")
		}
		if input.ExpectedGeneration != 2 {
			return deployment.View{}, &deployment.GenerationStaleError{CurrentGeneration: 2}
		}
		return deployment.View{}, providers.Builtin().ValidateSpecification(input.Provider, input.DeploymentSpec)
	}
	h := newTestHandler(t, deps)
	for _, tc := range []struct {
		provider           string
		generation, status int
		code               string
	}{{"docker", 1, 409, "sandbox_generation_stale"}, {"docker", 2, 400, "invalid_sandbox_configuration"}, {"e2b", 2, 200, ""}} {
		req := httptest.NewRequest(http.MethodPost, "/core/v1/sandbox/deployment", strings.NewReader(fmt.Sprintf(`{"provider":%q,"expected_generation":%d,"resources":{"cpus":2,"memory_mib":2048},"configuration":{}}`, tc.provider, tc.generation)))
		req.Header.Set("Authorization", "Bearer administrator")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.status || tc.code != "" && !strings.Contains(w.Body.String(), tc.code) {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
}
