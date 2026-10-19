package codex

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
)

func TestDeclarationAdmitsStructuredOutput(t *testing.T) {
	if !Declaration.Capabilities.StructuredOutput.IsSupported() {
		t.Fatal("structured output is not declared")
	}
	err := Declaration.ValidateConfiguration(harnessconfig.Admission{TextFormat: "json_schema", TextSchema: []byte(`{"type":"object"}`)})
	if err != nil {
		t.Fatalf("json_schema rejected: %v", err)
	}
}
