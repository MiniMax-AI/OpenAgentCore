package docker

import (
	"archive/tar"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimebootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/moby/moby/client"
)

func TestBootstrapDeliversOnlyPublicConnectionInput(t *testing.T) {
	b := sandbox.Bootstrap{Reference: sandbox.Reference{EnvironmentID: "0d1f4f1c-6c3e-4e0b-9a52-3e3d8b0e7a11"}, SessionID: "6a1b7c0e-2f4d-4c55-8f6e-9d2a1b3c4d5e", CoreURL: "https://core.example/api/v1", DeviceID: "da912024-1543-4242-a2c1-5f4f7ebbc6c7", Credential: "test-secret"}
	found := false
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
			if strings.Contains(h.Name, "auth.json") {
				t.Error("provider wrote Runtime private storage")
			}
			if h.Name == "runtime/runtime-bootstrap.json" {
				found = true
				raw, err := io.ReadAll(tr)
				if err != nil {
					t.Error(err)
				}
				c, err := runtimebootstrap.Decode(raw)
				if err != nil || c != b.RuntimeConnection() || h.Mode != 0600 || h.Uid != 1000 || h.Gid != 1000 {
					t.Error("invalid launch input or permissions")
				}
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
	if !found {
		t.Fatal("missing Runtime input")
	}
}
