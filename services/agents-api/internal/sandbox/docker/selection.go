package docker

import (
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

func Policy() sandbox.DeploymentPolicy { return sandbox.DeploymentPolicy{Runtime: true} }

func ValidateResources(r sandbox.Resources) error { return r.ValidatePolicy("docker", Policy()) }
func ValidateSpecification(s sandbox.DeploymentSpec) error {
	if err := ValidateResources(s.Resources); err != nil {
		return err
	}
	if s.Runtime == nil {
		return &sandbox.ValidationError{Param: "runtime", Message: fmt.Sprintf("%s: managed nodes require a pinned Runtime release", sandbox.ErrInvalid)}
	}
	return s.Runtime.Validate()
}
