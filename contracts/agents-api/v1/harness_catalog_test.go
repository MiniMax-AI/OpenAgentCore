package v1

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
)

func TestRegisteredHarnessesSharePublicValidation(t *testing.T) {
	for _, kind := range builtin.Kinds() {
		if err := (&AgentsCore{Harness: kind}).Validate(); err != nil {
			t.Fatalf("inline %s: %v", kind, err)
		}
		if err := (&SavedAgentCoreInput{Harness: kind}).Validate(); err != nil {
			t.Fatalf("saved %s: %v", kind, err)
		}
	}
	for _, kind := range []string{"unregistered", "Codex", " codex"} {
		if (&AgentsCore{Harness: kind}).Validate() == nil || (&SavedAgentCoreInput{Harness: kind}).Validate() == nil {
			t.Fatalf("unregistered Harness %q accepted", kind)
		}
	}
}
