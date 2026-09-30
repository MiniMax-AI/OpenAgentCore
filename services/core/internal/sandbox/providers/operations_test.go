package providers

import (
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
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
	if err := ValidateBinding(adapters["e2b"], &docker.Provider{}); !errors.Is(err, providercontract.ErrContract) {
		t.Fatal("registration differs from instance", err)
	}
}
