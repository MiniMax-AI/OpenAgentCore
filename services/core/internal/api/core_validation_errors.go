package api

import (
	"errors"
	"net/http"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/builtin"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// writeCoreValidationError is deliberately gated by the router marker. Shared
// store validators must retain the public and machine routes' existing errors.
func writeCoreValidationError(w http.ResponseWriter, err error) bool {
	if !isCoreErrorWriter(w) {
		return false
	}
	var field *store.AdminValidationError
	var configuration *store.SandboxConfigurationError
	switch {
	case errors.As(err, &field):
		details := CoreErrorDetails{"max_length": CoreErrorNumber(float64(field.MaxLength))}
		message := "The name exceeds its length limit or contains invalid characters."
		if field.Code == "invalid_node_capacity" {
			details = CoreErrorDetails{"min": CoreErrorNumber(1), "max": CoreErrorNumber(1000000)}
			message = "Node capacity must be positive, at most 1000000, and max_retained must be at least max_active."
		}
		writeCoreError(w, http.StatusBadRequest, field.Code, message, details, field.Param)
		return true
	case errors.As(err, &configuration) && configuration.Validation != nil:
		field := configuration.Validation
		details := CoreErrorDetails{}
		if field.Min != nil {
			details["min"] = CoreErrorNumber(float64(*field.Min))
		}
		if field.Max != nil {
			details["max"] = CoreErrorNumber(float64(*field.Max))
		}
		writeCoreError(w, http.StatusBadRequest, "invalid_sandbox_configuration", configuration.Message, details, field.Param)
		return true
	}
	return false
}

func writeCoreModelProviderError(w http.ResponseWriter, err error, harness string) bool {
	if !isCoreErrorWriter(w) {
		return false
	}
	var field *v1.ModelProviderError
	if !errors.As(err, &field) {
		return false
	}
	details := CoreErrorDetails{}
	if field.Code == "model_provider_api_key_invalid" {
		details["max_length"] = CoreErrorNumber(16384)
	}
	if field.Code == "model_provider_protocol_unsupported" {
		// Only adapter-owned catalog values may enter details, never a submitted
		// protocol or an unknown path harness.
		if configuration, known := builtin.Registry().Lookup(harness); known {
			protocols := make([]string, 0, len(configuration.Providers))
			for _, provider := range configuration.Providers {
				protocols = append(protocols, provider.Protocol)
			}
			details["harness"] = CoreErrorString(harness)
			details["allowed_protocols"] = CoreErrorStrings(protocols...)
		}
	}
	var param []string
	if field.Param != "" {
		param = []string{field.Param}
	}
	writeCoreError(w, http.StatusBadRequest, field.Code, field.Error(), details, param...)
	return true
}
