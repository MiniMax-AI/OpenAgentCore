package providers

import (
	"fmt"
	"regexp"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

// ValidateRegistration checks wiring before configuration parsing or construction.
// Only configuration requirements and operation declarations are read, and the
// resource validator only for a declared default size, after every other check.
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
	if a.ValidateSpecification == nil {
		return invalid("specification validator")
	}
	if a.ValidateResources == nil {
		return invalid("resource validator")
	}
	if err := validateConfigurationAdapter(a.Configuration); err != nil {
		return err
	}
	if (len(a.Policy.Artifacts) != 0) == (a.Policy.RuntimeError != "") {
		return invalid("Runtime input policy")
	}
	for name, rule := range a.Policy.Artifacts {
		pattern, err := regexp.Compile("^(?:" + rule.Pattern + ")$")
		if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(name) || err != nil || pattern.MatchString("") || len(rule.ManifestPath) == 0 {
			return invalid("Runtime artifact declaration")
		}
		for _, key := range rule.ManifestPath {
			if key == "" {
				return invalid("Runtime artifact manifest path")
			}
		}
	}
	if a.Operations == nil {
		return invalid("operation declaration")
	}
	operations := a.Operations()
	if err := sandbox.ValidateOperations(operations); err != nil {
		return err
	}
	// The common lifecycle suspends only node allocations, so only a nodes
	// registration may declare checkpoint support.
	if operations["Initial"].State == providercontract.Supported && a.Mode != sandbox.DeploymentNodes {
		return invalid("checkpoint support outside nodes mode")
	}
	if a.Policy.DefaultResources != nil && a.ValidateResources(*a.Policy.DefaultResources) != nil {
		return invalid("default resources")
	}
	return nil
}
