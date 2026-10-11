package smolvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
	sdk "github.com/smol-machines/smolvm-sdk/smolvm-go"
)

// TestLiveAllocation requires a local smolvm service with the machine-label and
// local OCI image APIs. It is opt-in because it boots a real VM and imports an
// OCI archive on the server host.
func TestLiveAllocation(t *testing.T) {
	socket, image := os.Getenv("OAC_SMOLVM_SOCKET"), os.Getenv("OAC_SMOLVM_IMAGE")
	if socket == "" || image == "" {
		t.Skip("set OAC_SMOLVM_SOCKET and OAC_SMOLVM_IMAGE to test a live VM")
	}
	archive, err := os.Open(image)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	sha := sha256.New()
	if _, err := archive.WriteTo(sha); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	root := filepath.Join(t.TempDir(), "receipts")
	provider, err := New(sdk.NewClient(socket), Config{
		InstallationID: uuid.NewString(), ImagePath: image,
		ImageHash: hex.EncodeToString(sha.Sum(nil)), ReceiptRoot: root,
		Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048, RootDiskMiB: 8192, EnvironmentDiskMiB: 8192},
	})
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := sandbox.Bootstrap{
		Reference: sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()},
		SessionID: uuid.NewString(), DeviceID: uuid.NewString(), CoreURL: "https://core.example/api/v1",
		Credential: "synthetic-live-test", NetworkAccess: "enabled",
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 90*time.Second)
		defer done()
		if err := provider.Kill(cleanup, bootstrap.Reference); err != nil {
			t.Errorf("Kill: %v", err)
		}
	}()
	created, err := provider.Create(ctx, bootstrap)
	if err != nil {
		t.Fatalf("Create: %+v: %v", created, err)
	}
	if !created.BootstrapComplete || created.ProviderID == "" {
		t.Fatalf("incomplete allocation: %+v", created)
	}
	observed, err := provider.GetInfo(ctx, bootstrap.Reference)
	if err != nil || !observed.BootstrapComplete {
		t.Fatalf("GetInfo: %+v: %v", observed, err)
	}
	if _, err := provider.Create(ctx, bootstrap); !errors.Is(err, sandbox.ErrExists) {
		t.Fatalf("duplicate Create: %v", err)
	}
	stdin := []byte{'a', 'b', 'c', 0, 'z', 0xff}
	result, err := provider.RunCommand(ctx, bootstrap.Reference, sandbox.Command{Args: []string{"/bin/cat"}, Stdin: stdin})
	if err != nil || result.ExitCode != 0 || !bytes.Equal([]byte(result.Stdout), stdin) {
		t.Fatalf("binary RunCommand: %+v: %v", result, err)
	}
	result, err = provider.RunCommand(ctx, bootstrap.Reference, sandbox.Command{Args: []string{"/bin/sh", "-c", "stat -c '%a %u' /home/runtime/runtime-bootstrap.json; id -u"}})
	if err != nil || result.ExitCode != 0 || result.Stdout != "600 1000\n1000\n" {
		t.Fatalf("bootstrap file and user: %+v: %v", result, err)
	}
}
