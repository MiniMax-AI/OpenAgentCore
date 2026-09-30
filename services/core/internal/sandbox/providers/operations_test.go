package providers

import (
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"testing"
)

func TestRegistrationRejectsMissingAndMismatchedDeclarations(t *testing.T) {
	registry := Builtin()
	for _, adapter := range []Adapter{{}, {Operations: func() providercontract.Operations { return nil }}} {
		registry.adapters["invalid-contract-fixture"] = adapter
		if _, err := registry.Lookup("invalid-contract-fixture"); err == nil {
			t.Fatal("invalid declaration registered")
		}
	}
	delete(registry.adapters, "invalid-contract-fixture")
	if err := ValidateBinding(registry.adapters["e2b"], &docker.Provider{}); !errors.Is(err, providercontract.ErrContract) {
		t.Fatal("registration differs from instance", err)
	}
}
