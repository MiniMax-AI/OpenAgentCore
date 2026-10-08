package microsandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

func TestMicrosandboxConstructionSelectsGenerationReadiness(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("microsandbox requires Linux")
	}
	useKVM(t, true)
	dir := t.TempDir()
	native := Native{HelperPath: filepath.Join(dir, "helper"), RuntimeHome: dir, RuntimePath: filepath.Join(dir, "msb"), FirmwarePath: filepath.Join(dir, "firmware"), Network: Network{DefaultEgress: "allow", DefaultIngress: "deny"}}
	raw, err := json.Marshal(native)
	if err != nil {
		t.Fatal(err)
	}
	script := []byte("#!/bin/sh\nprintf '%s' '{}'\n")
	digest := sha256.Sum256(script)
	release := sandbox.RuntimeRelease{MicrosandboxRef: "oac-runtime@sha256:" + strings.Repeat("d", 64), RuntimeSHA256: hex.EncodeToString(digest[:]), FirmwareSHA256: hex.EncodeToString(digest[:])}
	config := sandbox.NodeConfig{Provider: "microsandbox", Generation: 9, Specification: sandbox.DeploymentSpec{Resources: sandbox.Resources{CPUs: 2, MemoryMiB: 2048, RootDiskMiB: 8192, EnvironmentDiskMiB: 8192}, Runtime: &release}, Native: raw}
	for _, path := range []string{native.RuntimePath, native.FirmwarePath, native.HelperPath} {
		if err := os.WriteFile(path, script, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(native.RuntimeHome, 0700); err != nil {
		t.Fatal(err)
	}
	for _, options := range []sandbox.LocalOptions{{Standalone: true}, {GenerationStateDirectory: t.TempDir()}} {
		built := &sandbox.Built{InstallationID: uuid.NewString(), SpecificationDigest: config.Specification.Digest(config.Provider)}
		closeProvider, err := BuildNode(config, options, built)
		if err != nil {
			t.Fatal(err)
		}
		err = built.Probe(t.Context())
		closeProvider()
		if options.Standalone && err != nil || !options.Standalone && !errors.Is(err, sandbox.ErrRuntimeImageUnavailable) {
			t.Fatalf("readiness for %+v: %v", options, err)
		}
	}
}
