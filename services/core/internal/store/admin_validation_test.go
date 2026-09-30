package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

func TestAdminNameValidationIdentityAndBoundaries(t *testing.T) {
	for _, max := range []int{80, 128} {
		for _, name := range []string{"", " ", "private-name\t", strings.Repeat("a", max+1), string([]byte{0xff})} {
			_, err := adminResourceName(name, max)
			var field *AdminValidationError
			if !errors.As(err, &field) || !errors.Is(err, ErrInvalidInput) || field.Code != "invalid_name" || field.Param != "name" || field.MaxLength != max || err.Error() != fmt.Sprintf("%s: name must contain 1–%d characters without controls", ErrInvalidInput, max) {
				t.Fatalf("wrong name error: %#v", err)
			}
		}
		if got, err := adminResourceName(" "+strings.Repeat("界", max)+" ", max); err != nil || got != strings.Repeat("界", max) {
			t.Fatal("rune/trim semantics changed", err)
		}
	}
}

func TestRuntimeNodeValidationIdentityAndPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name             string
		active, retained int
		param            string
	}{
		{"", 0, 0, "name"}, {strings.Repeat("界", 43), 1, 1, "name"}, {"private\nname", 1, 1, "name"},
		{"node", 0, 0, "max_active"}, {"node", 1000001, 1000001, "max_active"}, {"node", 1000001, 8, "max_active"}, {"node", 2, 1, "max_retained"}, {"node", 1, 1000001, "max_retained"},
	} {
		err := validateRuntimeNode(tc.name, tc.active, tc.retained)
		var field *AdminValidationError
		if !errors.As(err, &field) || !errors.Is(err, ErrInvalidInput) || field.Param != tc.param || err.Error() != ErrInvalidInput.Error() {
			t.Fatalf("wrong capacity error: %#v", err)
		}
	}
	if err := validateRuntimeNode("node", 1000000, 1000000); err != nil {
		t.Fatal("inclusive active capacity bound changed", err)
	}
	// Node validation retains its historical byte bound and limited controls;
	// it must not silently adopt the stricter Project/key name validator.
	for _, name := range []string{strings.Repeat("a", 128), "node\tname", string([]byte{0xff})} {
		if err := validateRuntimeNode(name, 1, 1000000); err != nil {
			t.Fatal("node acceptance changed", err)
		}
	}
}

func TestSandboxValidationWrapperPreservesClassification(t *testing.T) {
	_, err := SandboxSetupForSelection("00000000-0000-4000-8000-000000000001", SandboxDeploymentSetupRequest{Provider: "docker"})
	var configuration *SandboxConfigurationError
	if !errors.As(err, &configuration) || !errors.Is(err, ErrInvalidInput) || configuration.Validation == nil || configuration.Validation.Param != "resources.cpus" {
		t.Fatalf("missing validation metadata: %#v", err)
	}
	// The wrapper historically unwraps only ErrInvalidInput. Retain that contract
	// while carrying the package-owned validation metadata separately.
	if errors.Is(err, sandbox.ErrInvalid) {
		t.Fatal("wrapper changed sentinel identity")
	}
	_, err = SandboxSetupForSelection("00000000-0000-4000-8000-000000000001", SandboxDeploymentSetupRequest{Provider: "e2b", DeploymentSpec: sandbox.DeploymentSpec{Runtime: &sandbox.RuntimeRelease{}}})
	if !errors.As(err, &configuration) || configuration.Validation == nil || configuration.Validation.Param != "runtime" || err.Error() != "invalid sandbox configuration: E2B Runtime is selected by its immutable template build" {
		t.Fatal("pending E2B validation order changed", err)
	}
}
