package docker

import (
	"archive/tar"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/contracttest"
	"github.com/google/uuid"
	"github.com/moby/moby/client"
)

func TestBootstrapDeliversOnlySandboxIOInput(t *testing.T) {
	b := contracttest.Bootstrap(sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()})
	var files []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || !strings.HasSuffix(r.URL.Path, "/containers/test/archive") {
			t.Errorf("unexpected Docker operation %s", r.URL.Path)
		}
		tr := tar.NewReader(r.Body)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				break
			}
			if h.Typeflag != tar.TypeReg {
				continue
			}
			files = append(files, h.Name)
			raw, err := io.ReadAll(tr)
			if err != nil {
				t.Error(err)
			}
			if h.Mode != 0600 || h.Uid != 1000 || h.Gid != 1000 {
				t.Error("launch input permissions", h.Name)
			}
			if in, err := sandboxbootstrap.Decode(raw); err != nil || in != b.SandboxIO {
				t.Error("invalid Sandbox I/O launch input")
			}
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	c, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.52"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := (&Provider{client: c}).bootstrap(t.Context(), "test", b); err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "runtime/sandbox-io-bootstrap.json" {
		t.Fatal("bootstrap files", files)
	}
}
