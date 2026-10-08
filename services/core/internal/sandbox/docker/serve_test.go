package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxfs"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/contracttest"
)

// A real Docker sandbox Serves its allocation over the Link: an Attach from a
// test agent host lists / through File.
func TestDockerSandboxServesItsAllocation(t *testing.T) {
	base := os.Getenv("AGENTS_RUNTIME_DOCKER_TEST_IMAGE")
	if base == "" {
		t.Skip("explicit Docker fixture image required")
	}
	seccomp, err := os.ReadFile("../../../../../deploy/distribution/seccomp.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.New(client.FromEnv)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	authority := sandboxlinktest.NewAuthority()
	links := relay.New(authority)
	t.Cleanup(func() { links.Close() })
	// The sandbox reaches the relay at the bridge network's gateway, an
	// address the test certificate carries; the image trusts that certificate.
	bridge, err := c.NetworkInspect(t.Context(), "bridge", client.NetworkInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var gateway netip.Addr
	for _, config := range bridge.Network.IPAM.Config {
		if config.Gateway.Is4() {
			gateway = config.Gateway
		}
	}
	if !gateway.IsValid() {
		t.Fatal("the bridge network has no IPv4 gateway")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1), gateway.AsSlice()}}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(links)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{certificate}, PrivateKey: key}}}
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Listener.Close()
	server.Listener = listener
	server.StartTLS()
	t.Cleanup(server.Close)
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	// Derive a labeled image that runs this tree's oac-sandbox-io and whose
	// only trust anchor is the test certificate.
	binary := filepath.Join(t.TempDir(), "oac-sandbox-io")
	build := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-o", binary, "github.com/MiniMax-AI/OpenAgentCore/apps/sandboxio/cmd/oac-sandbox-io")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build oac-sandbox-io: %v\n%s", err, output)
	}
	serveBinary, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	var content bytes.Buffer
	archive := tar.NewWriter(&content)
	for _, file := range []struct {
		name string
		mode int64
		data []byte
	}{{"etc/ssl/certs/ca-certificates.crt", 0o644, ca}, {"usr/local/bin/oac-sandbox-io", 0o555, serveBinary}} {
		if err := archive.WriteHeader(&tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(file.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	created, err := c.ContainerCreate(t.Context(), client.ContainerCreateOptions{Image: base, Config: &container.Config{Labels: map[string]string{"io.oac.test": "docker-serve"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.ContainerRemove(context.Background(), created.ID, client.ContainerRemoveOptions{})
	if _, err := c.CopyToContainer(t.Context(), created.ID, client.CopyToContainerOptions{DestinationPath: "/", Content: &content}); err != nil {
		t.Fatal(err)
	}
	image, err := c.ContainerCommit(t.Context(), created.ID, client.ContainerCommitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := c.ImageRemove(context.Background(), image.ID, client.ImageRemoveOptions{}); err != nil {
			t.Error(err)
		}
	})
	p, err := New(c, Config{InstallationID: uuid.NewString(), Image: image.ID, Network: "bridge", Seccomp: string(seccomp)})
	if err != nil {
		t.Fatal(err)
	}
	b := contracttest.Bootstrap(sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()})
	b.SandboxIO.LinkURL = "wss://" + net.JoinHostPort(gateway.String(), port) + "/api/v1/sandbox-link"
	resource := b.SandboxIO.Resource.Ref()
	authority.AddServe([]byte(b.SandboxIO.Credential), sandboxlink.ServePeer{PeerID: sandboxwire.NewID(), Resource: resource})
	runtimeID := sandboxwire.NewID()
	authority.AddRuntime([]byte("agent-host"), runtimeID)
	open := sandboxlink.Open{Resource: resource, Service: sandboxlink.ServiceFile, Version: sandboxfs.Version, AttachmentID: sandboxwire.NewID(),
		SessionID: sandboxwire.NewID(), AssignmentID: sandboxwire.NewID(), AssignmentEpoch: 1, AttachGrant: []byte("grant")}
	authority.AddGrant(open.AttachGrant, sandboxlinktest.Grant{RuntimeID: runtimeID, Resource: resource, SessionID: open.SessionID,
		AssignmentID: open.AssignmentID, AssignmentEpoch: 1, Services: []sandboxlink.Service{sandboxlink.ServiceFile}, Lease: time.Minute})

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if err := p.Kill(cleanup, b.Reference); err != nil {
			t.Error(err)
		}
	})
	if _, err := p.Create(ctx, b); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	link, err := sandboxlink.DialAttach(ctx, sandboxlink.AttachConfig{URL: "wss://127.0.0.1:" + port + "/api/v1/sandbox-link",
		TLS: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, RuntimeID: runtimeID, Credential: []byte("agent-host")})
	if err != nil {
		t.Fatal(err)
	}
	defer link.Close()
	// Open fails with ServiceUnavailable until the sandbox's serve peer connects.
	var stream sandboxlink.Stream
	for {
		stream, _, err = link.OpenService(ctx, open)
		if !errors.Is(err, sandboxlink.ServiceUnavailable) || ctx.Err() != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("open File:", err)
	}
	files := sandboxfs.NewClient(stream)
	defer files.Close()
	attached, err := files.Attach(ctx, &sandboxfs.AttachRequest{Export: sandboxfs.WorldExport})
	if err != nil {
		t.Fatal(err)
	}
	var handles sandboxfs.HandleIDs
	dir := handles.Next()
	if _, err := files.OpenDir(ctx, &sandboxfs.OpenDirRequest{Handle: dir, Node: attached.Root.Node}); err != nil {
		t.Fatal(err)
	}
	var names []string
	for cookie, end := uint64(0), false; !end; {
		listed, err := files.ReadDir(ctx, &sandboxfs.ReadDirRequest{Handle: dir, Cookie: cookie, Limit: 64 << 10})
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range listed.Entries {
			names, cookie = append(names, string(entry.Name)), entry.Cookie
		}
		end = listed.End
	}
	if !slices.Contains(names, "environment") || !slices.Contains(names, "workspace") {
		t.Fatalf("/ of the sandbox lists %q", names)
	}
}
