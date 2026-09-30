package store

import (
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/providers"
)

// SandboxConfigurationError contains only validated, non-secret configuration diagnostics.
type SandboxConfigurationError struct {
	Message    string
	Validation *sandbox.ValidationError
}

func sandboxConfigurationError(err error) *SandboxConfigurationError {
	var validation *sandbox.ValidationError
	errors.As(err, &validation)
	return &SandboxConfigurationError{Message: err.Error(), Validation: validation}
}

func (e *SandboxConfigurationError) Error() string { return e.Message }
func (e *SandboxConfigurationError) Unwrap() error { return ErrInvalidInput }

var ErrRuntimeSpecificationMismatch = errors.New("node does not match the deployment specification")

// SandboxSetupForSelection prepares metadata without writing or allocating resources.
func SandboxSetupForSelection(installationID string, input SandboxDeploymentSetupRequest) (SandboxSetup, error) {
	if _, err := parseConnectionGeneration(installationID); err != nil {
		return SandboxSetup{}, err
	}
	if err := validateSandboxSelection(input); err != nil {
		return SandboxSetup{}, err
	}
	normalized, err := providers.Normalize(input)
	if err != nil {
		return SandboxSetup{}, sandboxConfigurationError(err)
	}
	description, err := providers.Describe(input.Provider, installationID)
	if err != nil {
		return SandboxSetup{}, sandboxConfigurationError(err)
	}
	result := SandboxSetup{InstallationID: installationID, Provider: input.Provider, Mode: description.Mode, Specification: normalized.DeploymentSpec, E2B: normalized.E2B, BackendFingerprint: description.BackendFingerprint, IdleSeconds: description.IdleSeconds, RetentionSeconds: description.RetentionSeconds}
	return result, nil
}

func deploymentSpecification(d sqlc.RuntimeDeployment) (sandbox.DeploymentSpec, error) {
	var spec sandbox.DeploymentSpec
	if json.Unmarshal(d.Specification, &spec) != nil || providers.ValidateSpecification(d.ProviderKind, spec) != nil {
		return spec, ErrRuntimeSpecificationMismatch
	}
	return spec, nil
}
