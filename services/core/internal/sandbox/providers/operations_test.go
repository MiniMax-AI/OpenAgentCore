package providers

import (
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/e2b"
	"testing"
)

func TestRegistrationRejectsMissingAndMismatchedDeclarations(t *testing.T) {
	for _, adapter := range []Adapter{{}, {Operations: func() providercontract.Operations { return nil }}} {
		adapters["invalid-contract-fixture"] = adapter
		if _, err := Lookup("invalid-contract-fixture"); err == nil {
			t.Fatal("invalid declaration registered")
		}
	}
	delete(adapters, "invalid-contract-fixture")
	if err := ValidateBinding(Adapter{Operations: e2b.Operations}, &docker.Provider{}); !errors.Is(err, providercontract.ErrContract) {
		t.Fatal("registration differs from instance", err)
	}
}
