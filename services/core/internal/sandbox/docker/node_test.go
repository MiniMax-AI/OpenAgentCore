package docker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/moby/moby/client"
)

// The host's image store decides which release digest names the loaded image.
func TestNativeImageIsAReleaseIdentity(t *testing.T) {
	release := sandbox.RuntimeRelease{Artifacts: map[string]string{"image_id": "sha256:" + strings.Repeat("b", 64), "image_manifest_digest": "sha256:" + strings.Repeat("c", 64)}}
	config := sandbox.NodeConfig{Specification: sandbox.DeploymentSpec{Runtime: &release}}
	for _, image := range []string{release.Artifacts["image_id"], release.Artifacts["image_manifest_digest"]} {
		config.Native = json.RawMessage(`{"image":"` + image + `"}`)
		if entry, err := decodeNative(config); err != nil || entry.Image != image {
			t.Fatalf("rejected release image %s: %v", image, err)
		}
	}
	for _, native := range []string{`{"image":"sha256:` + strings.Repeat("a", 64) + `"}`, `{"image":"` + release.Artifacts["image_id"] + `","cpus":2}`, `{"image":null}`} {
		config.Native = json.RawMessage(native)
		if _, err := decodeNative(config); err == nil {
			t.Fatalf("accepted native %s", native)
		}
	}
}

func TestDockerProbeDiagnostics(t *testing.T) {
	image := "sha256:" + strings.Repeat("c", 64)
	for _, tc := range []struct {
		name                   string
		limits                 bool
		cpus                   int
		imageStatus            int
		want                   string
		unreachable, infoFails bool
	}{
		{name: "unreachable", unreachable: true, want: "provider_unavailable"},
		{name: "info", infoFails: true, want: "provider_unavailable"},
		// The pinned image is also missing below; earlier checks take precedence.
		{name: "limits", cpus: 8, imageStatus: 404, want: "host_unsupported"},
		{name: "capacity", limits: true, cpus: 1, imageStatus: 404, want: "capacity_insufficient"},
		{name: "image", limits: true, cpus: 8, imageStatus: 404, want: "runtime_image_unavailable"},
		{name: "image_inspect_fails", limits: true, cpus: 8, imageStatus: 500, want: "provider_unavailable"},
		{name: "ready", limits: true, cpus: 8, imageStatus: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch path := strings.TrimPrefix(r.URL.Path, "/v1.52"); {
				case path == "/_ping":
					_, _ = w.Write([]byte("OK"))
				case path == "/info" && !tc.infoFails:
					_ = json.NewEncoder(w).Encode(map[string]any{"MemoryLimit": tc.limits, "CpuCfsQuota": tc.limits, "NCPU": tc.cpus, "MemTotal": int64(64) << 30})
				case path == "/images/"+image+"/json" && tc.imageStatus == 200:
					_ = json.NewEncoder(w).Encode(map[string]string{"Id": image})
				case path == "/images/"+image+"/json":
					w.WriteHeader(tc.imageStatus)
					_, _ = w.Write([]byte(`{"message":"private daemon detail"}`))
				default:
					w.WriteHeader(500)
					_, _ = w.Write([]byte(`{"message":"private daemon detail"}`))
				}
			}))
			defer server.Close()
			host := server.URL
			if tc.unreachable {
				host = "unix://" + filepath.Join(t.TempDir(), "missing.sock")
			}
			c, err := client.New(client.WithHost(host), client.WithAPIVersion("1.52"))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if got := sandbox.NodeDiagnostic(dockerProbe(c, image, sandbox.Resources{CPUs: 2, MemoryMiB: 1024})(t.Context())); got != tc.want {
				t.Fatalf("diagnostic = %q, want %q", got, tc.want)
			}
		})
	}
}
