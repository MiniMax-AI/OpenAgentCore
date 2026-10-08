package execution

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
)

// A trusted registration is sufficient for the common deployment loader. Public
// configuration admission still validates its explicitly registered backend.
func TestSandboxProviderRegistrationDoesNotRequireAnExecutionVendorBranch(t *testing.T) {
	for _, mode := range []string{"nodes", "direct"} {
		t.Run(mode, func(t *testing.T) {
			id := uuid.NewString()
			config := &RuntimeProvider{InstallationID: id, ProviderKind: "contract-fixture", Mode: mode,
				SandboxLink: "wss://core.example/api/v1/sandbox-link", BackendFingerprint: strings.Repeat("a", 64), Provider: &lifecycleOnlySandbox{}}
			m, err := testManager(t, Owner{Lease: heldLease{}}, nil, nil, id, config.Provider)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { m.stop(); m.drain() })
			selectTestProvider(t, m, config)
			ready, err := m.ensureDeployment(t.Context())
			if err != nil || !ready {
				t.Fatalf("registered provider cannot enter common lifecycle: %v", err)
			}
			if m.config.ProviderKind != config.ProviderKind || m.config.Mode != mode || !reflect.DeepEqual(m.config.Provider.ProviderOperations(), config.Provider.ProviderOperations()) {
				t.Fatal("registration identity changed")
			}
		})
	}
}

// Methods must not run during registration; the loader only binds an adapter.
type lifecycleOnlySandbox struct{}

func (*lifecycleOnlySandbox) Create(context.Context, sandbox.Bootstrap) (sandbox.Info, error) {
	panic("registration created compute")
}
func (*lifecycleOnlySandbox) GetInfo(context.Context, sandbox.Reference) (sandbox.Info, error) {
	panic("registration read compute")
}
func (*lifecycleOnlySandbox) Renew(context.Context, sandbox.Reference) (sandbox.Info, error) {
	panic("registration renewed compute")
}
func (*lifecycleOnlySandbox) Kill(context.Context, sandbox.Reference) error {
	panic("registration killed compute")
}
