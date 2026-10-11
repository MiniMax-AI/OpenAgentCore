//go:build linux

package main

import (
	"strings"
	"testing"

	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

func TestOfficialSDKReleaseMatchesEmbeddedFFI(t *testing.T) {
	t.Setenv("MSB_HOME", t.TempDir())
	if sdkModuleVersion() == "" {
		t.Fatal("missing pinned SDK module identity")
	}
	if sdk.SDKVersion() != strings.TrimPrefix(wire.SDKVersion, "v") {
		t.Fatal("SDK source does not match admitted native release")
	}
	version, err := sdk.RuntimeVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != sdk.SDKVersion() {
		t.Fatalf("embedded FFI %q does not match SDK %q", version, sdk.SDKVersion())
	}
}
