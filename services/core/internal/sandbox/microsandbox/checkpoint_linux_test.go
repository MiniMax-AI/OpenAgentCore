//go:build linux

package microsandbox

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func privateCheckpointRoot(t *testing.T, marker string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".oac-checkpoint-store"), []byte(marker+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestCheckpointCompatibilityUsesStoreIdentityNotMountOrCachePath(t *testing.T) {
	c := testConfig()
	c.CheckpointRoot = privateCheckpointRoot(t, "11111111-1111-4111-8111-111111111111")
	a, err := CheckpointClass(c)
	if err != nil {
		t.Fatal(err)
	}
	c.CheckpointRoot = privateCheckpointRoot(t, "11111111-1111-4111-8111-111111111111")
	c.RuntimeHome = "/different/cache"
	c.RuntimePath = "/different/msb"
	c.FirmwarePath = "/different/firmware"
	b, err := CheckpointClass(c)
	if err != nil || a != b {
		t.Fatal("host paths changed compatibility", a, b, err)
	}
	c.MemoryMiB++
	b, err = CheckpointClass(c)
	if err != nil || a.ExecutionClass == b.ExecutionClass || a.ArtifactDomain != b.ArtifactDomain {
		t.Fatal("geometry not fenced", a, b, err)
	}
	c.CheckpointRoot = privateCheckpointRoot(t, "22222222-2222-4222-8222-222222222222")
	b, err = CheckpointClass(c)
	if err != nil || a.ArtifactDomain == b.ArtifactDomain {
		t.Fatal("foreign store matched", a, b, err)
	}
}
func TestCheckpointStoreRequiresPrivateCanonicalIdentity(t *testing.T) {
	c := testConfig()
	c.CheckpointRoot = privateCheckpointRoot(t, "11111111-1111-4111-8111-111111111111")
	if err := os.Chmod(c.CheckpointRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckpointStore(c); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatal(err)
	}
	if err := os.Chmod(c.CheckpointRoot, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(c.CheckpointRoot, link); err != nil {
		t.Fatal(err)
	}
	c.CheckpointRoot = link
	if _, err := CheckpointStore(c); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatal(err)
	}
	c.CheckpointRoot = privateCheckpointRoot(t, "00000000-0000-0000-0000-000000000000")
	if _, err := CheckpointStore(c); !errors.Is(err, sandbox.ErrOwnership) {
		t.Fatal(err)
	}
}

func TestCPUCompatibilityPreservesHeterogeneousProcessorProfiles(t *testing.T) {
	first := "processor:0\nmodel:1\nflags:a b\n\nprocessor:1\nmodel:2\nflags:c d\n"
	reordered := "processor:9\nflags:d c\nmodel:2\n\nprocessor:4\nflags:b a\nmodel:1\n\nprocessor:5\nflags:a b\nmodel:1\n"
	crossed := "processor:0\nmodel:1\nflags:c d\n\nprocessor:1\nmodel:2\nflags:a b\n"
	if !reflect.DeepEqual(checkpointCPUProfiles(first), checkpointCPUProfiles(reordered)) {
		t.Fatal("processor order/count affected class")
	}
	if reflect.DeepEqual(checkpointCPUProfiles(first), checkpointCPUProfiles(crossed)) {
		t.Fatal("heterogeneous CPU associations collapsed")
	}
}
