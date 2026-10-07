package sandbox_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/contracttest"
)

// Bootstrap.Validate is the one gate Core applies before Create.
func TestBootstrapValidateRejections(t *testing.T) {
	ref := sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}
	if err := contracttest.Bootstrap(ref).Validate(); err != nil {
		t.Fatal("valid bootstrap rejected:", err)
	}
	for name, change := range map[string]func(*sandbox.Bootstrap){
		"noncanonical tenant":        func(b *sandbox.Bootstrap) { b.TenantID = strings.ToUpper(b.TenantID) },
		"nil session":                func(b *sandbox.Bootstrap) { b.SessionID = uuid.Nil.String() },
		"missing device":             func(b *sandbox.Bootstrap) { b.DeviceID = "" },
		"Core URL off the API base":  func(b *sandbox.Bootstrap) { b.CoreURL = "https://core.example" },
		"empty Runtime credential":   func(b *sandbox.Bootstrap) { b.Credential = "" },
		"unknown network access":     func(b *sandbox.Bootstrap) { b.NetworkAccess = "sometimes" },
		"plain ws Link off loopback": func(b *sandbox.Bootstrap) { b.SandboxIO.LinkURL = "ws://core.example/api/v1/sandbox-link" },
		"empty Serve credential":     func(b *sandbox.Bootstrap) { b.SandboxIO.Credential = "" },
		"zero generation":            func(b *sandbox.Bootstrap) { b.SandboxIO.Resource.Generation = 0 },
		"enrollment resource":        func(b *sandbox.Bootstrap) { b.SandboxIO.Resource.Kind = "enrollment" },
		"another allocation":         func(b *sandbox.Bootstrap) { b.SandboxIO.Resource.ID = uuid.NewString() },
		"another tenant":             func(b *sandbox.Bootstrap) { b.SandboxIO.Resource.TenantID = uuid.NewString() },
		"another Environment":        func(b *sandbox.Bootstrap) { b.SandboxIO.Resource.EnvironmentID = uuid.NewString() },
	} {
		b := contracttest.Bootstrap(ref)
		change(&b)
		if err := b.Validate(); !errors.Is(err, sandbox.ErrInvalid) {
			t.Errorf("%s: Validate = %v", name, err)
		}
	}
}
