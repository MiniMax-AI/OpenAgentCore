package smolvm

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
	sdk "github.com/smol-machines/smolvm-sdk/smolvm-go"
)

func testProvider(t *testing.T, handler http.HandlerFunc) (*Provider, sandbox.Reference, *int) {
	t.Helper()
	calls := new(int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { *calls++; handler(w, r) }))
	t.Cleanup(server.Close)
	image := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(image, []byte("archive"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := New(sdk.NewClient(server.URL), Config{InstallationID: uuid.NewString(), ImagePath: image,
		ImageHash: strings.Repeat("a", 64), ReceiptRoot: filepath.Join(t.TempDir(), "private"),
		Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 1024, RootDiskMiB: 1024, EnvironmentDiskMiB: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	ref := sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}
	return p, ref, calls
}

func TestForeignMachineCannotBeObservedOrDeleted(t *testing.T) {
	p, ref, calls := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected native mutation: %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"name":"foreign","labels":{"io.oac.tenant":"foreign"},"state":"running"}`))
	})
	if _, err := p.GetInfo(context.Background(), ref); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatalf("GetInfo: %v", err)
	}
	if err := p.Kill(context.Background(), ref); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatalf("Kill: %v", err)
	}
	if *calls != 2 {
		t.Fatalf("native calls=%d want 2 read-only", *calls)
	}
}

func TestDiskGiBRoundsWithoutOverflow(t *testing.T) {
	for _, tc := range []struct {
		mib uint32
		gib int64
	}{{1, 1}, {1024, 1}, {1025, 2}, {^uint32(0), 4194304}} {
		if got := diskGiB(tc.mib); got != tc.gib {
			t.Fatalf("diskGiB(%d) = %d, want %d", tc.mib, got, tc.gib)
		}
	}
}

func TestBootstrapReceiptNeverReplacesAnotherNativeMachine(t *testing.T) {
	p, ref, _ := testProvider(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	first, second := uuid.NewString(), uuid.NewString()
	if err := p.writeReceipt(ref, first); err != nil {
		t.Fatal(err)
	}
	if err := p.writeReceipt(ref, second); !errors.Is(err, sandbox.ErrExists) {
		t.Fatalf("overwriting receipt: %v", err)
	}
	got, err := p.readReceipt(ref)
	if err != nil || got.NativeID != first {
		t.Fatalf("original receipt lost: %+v %v", got, err)
	}
}

func TestReceiptRootRejectsSymlinks(t *testing.T) {
	p, _, _ := testProvider(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(p.config.ReceiptRoot, link); err != nil {
		t.Fatal(err)
	}
	config := p.config
	config.ReceiptRoot = link
	if _, err := New(p.client, config); !errors.Is(err, sandbox.ErrInvalid) {
		t.Fatalf("symlink receipt root: %v", err)
	}
}

func TestPrivateAPISocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sock")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	address := "unix://" + path
	if !errors.Is(privateSocket(address), sandbox.ErrInvalid) {
		t.Fatal("regular file accepted as an API socket")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(privateSocket(address), sandbox.ErrInvalid) {
		t.Fatal("public API socket accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := privateSocket(address); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "linked.sock")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(privateSocket("unix://"+link), sandbox.ErrInvalid) {
		t.Fatal("socket symlink accepted")
	}
}
