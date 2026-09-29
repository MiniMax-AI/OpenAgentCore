package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
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
	result := SandboxSetup{InstallationID: installationID, Provider: input.Provider,
		Mode: "nodes", Specification: input.DeploymentSpec, E2B: input.E2B}
	namespace := "nodes:" + installationID
	if input.Provider == "e2b" {
		result.Mode = "direct"
		namespace = "e2b:" + installationID
	}
	if input.Provider == "microsandbox" {
		result.IdleSeconds, result.RetentionSeconds = 300, 86400
	}
	digest := sha256.Sum256([]byte(input.Provider + "\x00" + namespace))
	result.BackendFingerprint = hex.EncodeToString(digest[:])
	return result, nil
}

func deploymentSpecification(d sqlc.RuntimeDeployment) (sandbox.DeploymentSpec, error) {
	var spec sandbox.DeploymentSpec
	if json.Unmarshal(d.Specification, &spec) != nil || spec.Validate(d.ProviderKind) != nil {
		return spec, ErrRuntimeSpecificationMismatch
	}
	return spec, nil
}
