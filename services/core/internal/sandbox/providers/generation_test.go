package providers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	sandboxmicro "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	"github.com/google/uuid"
)

func generationConfig(t *testing.T) (sandbox.NodeConfig, sandboxmicro.Native) {
	t.Helper()
	dir := t.TempDir()
	spec := validRegistrationSpec()
	spec.Runtime.Artifacts = map[string]string{"microsandbox_ref": "oac-runtime@sha256:" + strings.Repeat("d", 64), "runtime_sha256": strings.Repeat("e", 64), "firmware_sha256": strings.Repeat("f", 64)}
	spec.Resources.RootDiskMiB, spec.Resources.EnvironmentDiskMiB = 8192, 8192
	native := sandboxmicro.Native{HelperPath: filepath.Join(dir, "helper"), RuntimeHome: dir, RuntimePath: filepath.Join(dir, "msb"), FirmwarePath: filepath.Join(dir, "firmware"),
		Network: sandboxmicro.Network{DefaultEgress: "allow", DefaultIngress: "deny"}}
	raw, err := json.Marshal(native)
	if err != nil {
		t.Fatal(err)
	}
	return sandbox.NodeConfig{Provider: "microsandbox", InstallationID: uuid.NewString(), Generation: 9, Specification: spec, Native: raw}, native
}

func TestMicrosandboxGenerationDirectoryOwnership(t *testing.T) {
	registry := Builtin()
	for _, mode := range []string{"private", "public", "symlink", "file"} {
		t.Run(mode, func(t *testing.T) {
			state := t.TempDir()
			directory := filepath.Join(state, "generations")
			switch mode {
			case "public":
				if err := os.Mkdir(directory, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(directory, 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(t.TempDir(), directory); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(directory, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			config, _ := generationConfig(t)
			built, closeProvider, err := registry.Build(config, sandbox.LocalOptions{GenerationStateDirectory: state})
			closeProvider()
			if mode == "private" {
				info, statErr := os.Lstat(directory)
				if err != nil || built == nil || statErr != nil || info.Mode().Perm() != 0700 {
					t.Fatalf("private generation: %v, %v", err, statErr)
				}
			} else if err == nil || built != nil {
				t.Fatalf("accepted unsafe directory: %v", err)
			} else if mode != "file" && !errors.Is(err, sandbox.ErrOwnership) {
				t.Fatalf("ownership error changed: %v", err)
			}
		})
	}
}

// Exercise the actual ProcessCaller boundary to prove the constructor binds the
// lease to this generation, installation and specification before any helper runs.
func TestMicrosandboxGenerationBindsLeaseIdentity(t *testing.T) {
	registry := Builtin()
	config, native := generationConfig(t)
	state := t.TempDir()
	built, closeProvider, err := registry.Build(config, sandbox.LocalOptions{GenerationStateDirectory: state})
	if err != nil {
		t.Fatal(err)
	}
	defer closeProvider()
	if built.Quiescent == nil || !built.Quiescent() {
		t.Fatal("generation does not report helper quiescence")
	}
	response, err := json.Marshal(sandboxmicro.Response{Version: sandboxmicro.ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n[ \"$OAC_NODE_GENERATION_LEASE_FD\" = 3 ] || exit 1\ncat >/dev/null\nprintf '%s' '" + string(response) + "'\n"
	if err := os.WriteFile(native.HelperPath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(state, "generations", strconv.FormatUint(config.Generation, 10))
	leasePath := base + ".lease"
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ref := sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}
	if err := built.Provider.Kill(ctx, ref); !errors.Is(err, sandbox.ErrComputeUnconfirmed) {
		t.Fatalf("missing lease accepted: %v", err)
	}
	if err := os.WriteFile(leasePath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(leasePath)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	record := map[string]any{"installation_id": config.InstallationID, "generation": config.Generation, "specification_digest": built.SpecificationDigest, "device": uint64(st.Dev), "inode": st.Ino}
	writeJSON := func(path string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSON(base+".json", config)
	for _, field := range []string{"installation_id", "generation", "specification_digest", "inode"} {
		previous := record[field]
		record[field] = "foreign"
		writeJSON(base+".lease-identity", record)
		if err := built.Provider.Kill(ctx, ref); !errors.Is(err, sandbox.ErrComputeUnconfirmed) {
			t.Fatalf("accepted mismatched %s: %v", field, err)
		}
		record[field] = previous
	}
	writeJSON(base+".lease-identity", record)
	if err := built.Provider.Kill(ctx, ref); err != nil {
		t.Fatalf("owned lease failed: %v", err)
	}
	// Closing a provider never removes durable generation ownership records.
	closeProvider()
	for _, suffix := range []string{".lease", ".lease-identity", ".json"} {
		if _, err := os.Stat(base + suffix); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(leasePath, 0644); err != nil {
		t.Fatal(err)
	}
	if err := built.Provider.Kill(ctx, ref); !errors.Is(err, sandbox.ErrComputeUnconfirmed) {
		t.Fatalf("accepted public lease: %v", err)
	}
}

func TestMicrosandboxGenerationRejectsUnpinnedImage(t *testing.T) {
	registry := Builtin()
	for _, image := range []string{"latest", "oac-runtime@sha256:bad", "oac-runtime@sha256:"} {
		t.Run(image, func(t *testing.T) {
			config, _ := generationConfig(t)
			config.Specification.Runtime.Artifacts["microsandbox_ref"] = image
			built, closeProvider, err := registry.Build(config, sandbox.LocalOptions{GenerationStateDirectory: t.TempDir()})
			closeProvider()
			if err == nil || built != nil {
				t.Fatalf("accepted image %q", image)
			}
		})
	}
}

func TestLocalConstructionRequiresExplicitContext(t *testing.T) {
	registry := Builtin()
	state := t.TempDir()
	config, _ := generationConfig(t)
	for _, options := range []sandbox.LocalOptions{
		{},
		{Standalone: true, GenerationStateDirectory: state},
		{GenerationStateDirectory: "relative"},
		{GenerationStateDirectory: state + "/../node"},
	} {
		built, closeProvider, err := registry.Build(config, options)
		closeProvider()
		if !errors.Is(err, sandbox.ErrInvalid) || built != nil {
			t.Fatalf("accepted construction context %+v: %v", options, err)
		}
	}
	built, closeProvider, err := registry.Build(config, sandbox.LocalOptions{Standalone: true})
	closeProvider()
	if err != nil || built == nil {
		t.Fatalf("standalone construction: %v", err)
	}
}
