package deployment

import (
	"errors"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
)

var (
	// ErrInvalidInput reports a malformed identifier, limit or request value.
	ErrInvalidInput = errors.New("invalid sandbox deployment input")
	// ErrNotFound reports a missing sandbox node, allocation or generation.
	ErrNotFound = errors.New("sandbox deployment resource not found")
	// ErrConflict reports a deployment that cannot change in its current state.
	ErrConflict = errors.New("sandbox deployment is already configured differently")
	// ErrSpecificationMismatch reports a node or stored generation whose
	// specification differs from the deployment's.
	ErrSpecificationMismatch = errors.New("node does not match the deployment specification")
	ErrResetInProgress       = errors.New("a sandbox reset is in progress")
	ErrNotConfigured         = errors.New("the sandbox deployment is not configured")
	ErrNodeInUse             = errors.New("sandbox node retains resources")
	ErrNodeCredential        = errors.New("invalid sandbox node credential")
	ErrLocalNodeConfigured   = errors.New("local sandbox node is enabled in deployment configuration")
	// ErrAllocationConflict rejects an allocation change whose owner no longer
	// matches the stored allocation, device binding, state or compute revision,
	// or a replay for another installation.
	ErrAllocationConflict = errors.New("the sandbox allocation changed")
	// ErrNodeAddressMismatch rejects an enrollment whose Core address is not
	// the installation public URL. The token stays unconsumed.
	ErrNodeAddressMismatch = errors.New("sandbox node Core address differs from the public URL")
	// ErrNodeExists rejects an enrollment whose node ID is already in use.
	ErrNodeExists = errors.New("sandbox node ID is already enrolled")
)

// GenerationStaleError rejects a change whose expected generation is not the
// deployment's current one.
type GenerationStaleError struct{ CurrentGeneration uint64 }

func (e *GenerationStaleError) Error() string { return "the sandbox deployment generation changed" }
func (e *GenerationStaleError) Unwrap() error { return ErrConflict }

// ResetRequiredError rejects a change that needs a reset first, such as a
// change of backend.
type ResetRequiredError struct{ CurrentProvider, RequestedProvider string }

func (e *ResetRequiredError) Error() string {
	return "reset the sandbox deployment before changing its backend"
}
func (e *ResetRequiredError) Unwrap() error { return ErrConflict }

// InUseError rejects reset completion while hosted resources still belong to
// the deployment.
type InUseError struct{ Resources Resources }

func (e *InUseError) Error() string {
	return "hosted sandbox resources still belong to this deployment"
}
func (e *InUseError) Unwrap() error { return ErrConflict }

// ConfigurationError contains only validated, non-secret configuration
// diagnostics.
type ConfigurationError struct {
	Message    string
	Validation *sandbox.ValidationError
}

func (e *ConfigurationError) Error() string { return e.Message }
func (e *ConfigurationError) Unwrap() error { return ErrInvalidInput }

// configurationError wraps a provider's rejection of a selection.
func configurationError(err error) *ConfigurationError {
	var validation *sandbox.ValidationError
	errors.As(err, &validation)
	return &ConfigurationError{Message: err.Error(), Validation: validation}
}

// NodeValidationError rejects a node name or capacity. Code is
// invalid_name or invalid_node_capacity, and Param names the field. It adds
// field identity without changing the ErrInvalidInput text or classification.
type NodeValidationError struct {
	Code, Param string
	MaxLength   int
}

func (e *NodeValidationError) Error() string { return ErrInvalidInput.Error() }
func (e *NodeValidationError) Unwrap() error { return ErrInvalidInput }
