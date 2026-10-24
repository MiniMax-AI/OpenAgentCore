package providers

import (
	"fmt"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
)

// ValidateRegistration checks wiring before configuration parsing or construction.
// Only configuration requirements, the setup declaration and operation
// declarations are read, and the policy only for a declared default size, after
// every other check.
func ValidateRegistration(a Adapter) error {
	invalid := func(field string) error {
		return fmt.Errorf("%w: invalid registration %s", providercontract.ErrContract, field)
	}
	switch a.Mode {
	case sandbox.DeploymentNodes:
		if err := validateNodeArtifacts(a.NodeArtifacts); err != nil {
			return err
		}
		if a.BuildLocal == nil || a.BuildDirect != nil {
			return invalid("node constructor")
		}
	case sandbox.DeploymentDirect:
		if len(a.NodeArtifacts) != 0 {
			return invalid("direct node artifacts")
		}
		if a.BuildDirect == nil || a.BuildLocal != nil {
			return invalid("direct constructor")
		}
	default:
		return invalid("mode")
	}
	if err := validateConfigurationAdapter(a.Configuration); err != nil {
		return err
	}
	if a.Setup.Validate(a.Configuration.Requirements()) != nil {
		return invalid("setup declaration")
	}
	if a.Policy.Workspace != nil && a.Policy.Workspace.Attachment != workspacefs.AttachmentHostDirectory {
		return invalid("workspace attachment requirements")
	}
	if a.Operations == nil {
		return invalid("operation declaration")
	}
	operations := a.Operations()
	if err := sandbox.ValidateOperations(operations); err != nil {
		return err
	}
	if a.Policy.DefaultResources != nil && a.Policy.DefaultResources.ValidatePolicy("", a.Policy) != nil {
		return invalid("default resources")
	}
	return nil
}
