package providers

import (
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// ValidateRegistration checks wiring before configuration parsing or construction.
// Only configuration requirements and operation declarations are read.
func ValidateRegistration(a Adapter) error {
	invalid := func(field string) error {
		return fmt.Errorf("%w: invalid registration %s", providercontract.ErrContract, field)
	}
	switch a.Mode {
	case "nodes":
		if a.BuildLocal == nil || a.BuildDirect != nil {
			return invalid("node constructor")
		}
	case "direct":
		if a.BuildDirect == nil || a.BuildLocal != nil {
			return invalid("direct constructor")
		}
	default:
		return invalid("mode")
	}
	if a.ValidateSpecification == nil {
		return invalid("specification validator")
	}
	if a.ValidateResources == nil {
		return invalid("resource validator")
	}
	if err := validateConfigurationAdapter(a.Configuration); err != nil {
		return err
	}
	if a.Policy.Runtime == (a.Policy.RuntimeError != "") {
		return invalid("Runtime input policy")
	}
	if a.Operations == nil {
		return invalid("operation declaration")
	}
	operations := a.Operations()
	if err := sandbox.ValidateOperations(operations); err != nil {
		return err
	}
	// The current common lifecycle admits checkpoint suspension only on nodes,
	// and creates its policy whenever checkpoint support is declared.
	if operations["Initial"].State == providercontract.Supported {
		const maximumSeconds = int64((1<<63 - 1) / time.Second)
		if a.Mode != "nodes" || a.IdleSeconds < 1 || a.RetentionSeconds < 1 ||
			a.IdleSeconds > maximumSeconds || a.RetentionSeconds > maximumSeconds {
			return invalid("checkpoint policy")
		}
	} else if a.IdleSeconds != 0 || a.RetentionSeconds != 0 {
		return invalid("non-checkpoint policy")
	}
	return nil
}
