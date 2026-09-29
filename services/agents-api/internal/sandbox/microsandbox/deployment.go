package microsandbox

import (
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

func Policy() sandbox.DeploymentPolicy { return sandbox.DeploymentPolicy{Disk: true, Runtime: true} }

func ValidateResources(r sandbox.Resources) error { return r.ValidatePolicy("microsandbox", Policy()) }
func ValidateSpecification(s sandbox.DeploymentSpec) error {
	return s.ValidatePolicy("microsandbox", Policy())
}
