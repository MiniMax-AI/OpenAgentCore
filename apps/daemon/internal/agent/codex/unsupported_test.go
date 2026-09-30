package codex

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

// Nil concrete receivers prove Unsupported needs no native owner, transport,
// workspace access or input retention. Callers still preserve the separate
// nil-Turn ownership rule on required lifecycle methods.
func TestUnsupportedExtensionsHaveNoNativeEffects(t *testing.T) {
	ctx := context.Background()
	const secret = "private-fixture-value"
	check := func(err error) {
		t.Helper()
		if !errors.Is(err, agent.ErrUnsupportedOperation) || err.Error() == agent.ErrUnsupportedOperation.Error() {
			t.Fatalf("expected explicit unsupported result with reason, got %v", err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatal("unsupported error disclosed input")
		}
	}
	for _, owner := range []agent.WorkspaceReader{(*Executor)(nil), (*Session)(nil), (*Prepared)(nil)} {
		result, err := owner.ReadWorkspaceFile(ctx, secret, 1)
		check(err)
		if len(result.Data) != 0 || result.Truncated {
			t.Fatal("unsupported read fabricated data")
		}
	}
	for _, owner := range []agent.WorkspaceDirectoryLister{(*Executor)(nil), (*Session)(nil), (*Prepared)(nil)} {
		result, err := owner.ListWorkspaceDirectory(ctx, secret, 1)
		check(err)
		if len(result.Entries) != 0 || result.Truncated {
			t.Fatal("unsupported listing fabricated entries")
		}
	}
	for _, owner := range []agent.WorkspaceWriter{(*Executor)(nil), (*Session)(nil), (*Prepared)(nil)} {
		result, err := owner.WriteWorkspaceFile(ctx, secret, []byte(secret))
		check(err)
		if result.SizeBytes != 0 {
			t.Fatal("unsupported write fabricated receipt")
		}
	}
}
