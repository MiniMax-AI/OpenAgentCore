package store_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func nativeDispatchHarness(t *testing.T) (*dispatchHarness, context.Context, string) {
	t.Helper()
	return nativeDispatchHarnessWithTimeout(t, 120*time.Second)
}

func nativeDispatchHarnessWithTimeout(t *testing.T, timeout time.Duration) (*dispatchHarness, context.Context, string) {
	t.Helper()
	binary, root := os.Getenv("OAC_TEST_NATIVE_DAEMON_BIN"), os.Getenv("OAC_TEST_NATIVE_PROOF_DIR")
	if binary == "" || root == "" {
		t.Skip("explicit native daemon binary and evidence directory required")
	}
	h := newDispatchHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	home, err := os.MkdirTemp(root, "execution-native-")
	if err != nil {
		t.Fatal(err)
	}
	startNativeDispatchDaemon(t, h, home, binary)
	return h, ctx, home
}

func startNativeDispatchDaemon(t *testing.T, h *dispatchHarness, home, binary string) {
	t.Helper()
	oldPeer, _ := h.registry.LookupDevice(h.device.ID)
	if h.conn != nil {
		_ = h.conn.Close()
	}
	profile := filepath.Join(home, "daemon", "execution")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	auth, _ := json.Marshal(map[string]string{"server_url": h.url + "/api/v1", "runtime_id": h.device.ID, "runner_credential": h.credential, "device_name": "native proof"})
	if err := os.WriteFile(filepath.Join(profile, "auth.json"), auth, 0600); err != nil {
		t.Fatal(err)
	}
	daemonLog, err := os.Create(filepath.Join(home, "daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daemonLog.Close() })
	cmd := exec.Command(binary, "connect", "--profile", "execution")
	// The device trusts the synthetic model server's certificate, which
	// nativeModelServer writes before the first Turn starts Codex.
	cmd.Env = append(os.Environ(), "OAC_RUNTIME_HOME="+home, "CODEX_CA_CERTIFICATE="+nativeModelCertificate(home))
	cmd.Stdout, cmd.Stderr = daemonLog, daemonLog
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-stopped:
		case <-time.After(8 * time.Second):
			_ = cmd.Process.Kill()
			<-stopped
		}
	})
	deadline := time.Now().Add(20 * time.Second)
	for {
		peer, e := h.registry.LookupDevice(h.device.ID)
		if e == nil && peer != oldPeer {
			if info, found, known := peer.AgentKindStatus("codex"); known && found && info.Available && info.Capabilities.EnvironmentNone {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("native daemon not ready; logs %s", home)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func nativeModelCertificate(home string) string { return filepath.Join(home, "model-ca.pem") }

// nativeModelServer serves a synthetic model over HTTPS, as the model provider
// contract requires, and writes the issuing CA for the native daemon to trust.
// Codex verifies with webpki, which rejects the self-signed CA certificate that
// httptest serves by default, so the server gets a leaf issued by a test CA.
func nativeModelServer(t *testing.T, home string, handler http.Handler) *httptest.Server {
	t.Helper()
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic model CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "127.0.0.1"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nativeModelCertificate(home), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600); err != nil {
		t.Fatal(err)
	}
	model := httptest.NewUnstartedServer(handler)
	model.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}}}
	model.StartTLS()
	return model
}

// nativeModelProvider is the provider bundle that reaches a synthetic model server.
func nativeModelProvider(model *httptest.Server) *v1.ModelProviderInput {
	return &v1.ModelProviderInput{Protocol: "responses", BaseURL: model.URL + "/v1", APIKey: "synthetic-test-token"}
}

// nativeDeploymentDefaults makes provider the deployment default model provider,
// which public environment:none Sessions freeze at creation.
func nativeDeploymentDefaults(s *store.Store, model string, provider *v1.ModelProviderInput) func(*api.Dependencies) {
	revision := uuid.New()
	return modelProviderDefaults(s, func(context.Context, string) (*store.DeploymentModelProviderSnapshot, error) {
		return &store.DeploymentModelProviderSnapshot{Model: model, Provider: provider, Revision: revision}, nil
	})
}

// readNativeModelDefaults reads a private real-model file holding exactly the
// deployment default an operator would configure: {"model", "model_provider"}.
func readNativeModelDefaults(t *testing.T, path string) (string, *v1.ModelProviderInput) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var defaults struct {
		Model         string                 `json:"model"`
		ModelProvider *v1.ModelProviderInput `json:"model_provider"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&defaults) != nil || decoder.Decode(new(any)) != io.EOF || defaults.ModelProvider == nil {
		t.Fatal("private options must hold only model and model_provider")
	}
	if defaults.ModelProvider.Validate() != nil {
		t.Fatal("invalid private model_provider")
	}
	if defaults.Model == "" {
		t.Fatal("real model required")
	}
	return defaults.Model, defaults.ModelProvider
}
