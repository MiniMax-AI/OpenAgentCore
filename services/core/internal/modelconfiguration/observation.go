package modelconfiguration

import "slices"

type ProviderErrorCode string

const (
	ProviderAuthenticationError ProviderErrorCode = "authentication_error"
	ProviderConnectionFailed    ProviderErrorCode = "connection_failed"
	ProviderRateLimitExceeded   ProviderErrorCode = "rate_limit_exceeded"
	ProviderUsageLimitExceeded  ProviderErrorCode = "usage_limit_exceeded"
	ProviderServerOverloaded    ProviderErrorCode = "server_overloaded"
	ProviderServerError         ProviderErrorCode = "server_error"
	ProviderResourceNotFound    ProviderErrorCode = "resource_not_found"
	ProviderRequestTimeout      ProviderErrorCode = "request_timeout"
	ProviderInvalidRequest      ProviderErrorCode = "invalid_request"
)

// Observation names a root Turn whose committed outcome may update the
// last-use observations of the deployment default its Session froze.
type Observation struct {
	TenantID, SessionID, TurnID string
}

// providerErrorCodes are the engine failures that describe the model provider
// itself. Context-length and cyber-policy failures describe the request. The
// observation statement's fence lists the same codes; testdata holds the cases
// both are checked against.
var providerErrorCodes = []ProviderErrorCode{ProviderAuthenticationError, ProviderConnectionFailed, ProviderRateLimitExceeded, ProviderUsageLimitExceeded, ProviderServerOverloaded, ProviderServerError, ProviderResourceNotFound, ProviderRequestTimeout, ProviderInvalidRequest}

// ShouldObserveProvider reports whether a committed root Turn outcome says
// something about its model provider: every completed Turn, and a failed Turn
// whose engine reported a provider error. It only spares a database round trip;
// the observation statement re-checks the committed outcome.
func ShouldObserveProvider(status, errorCode, engineErrorCode string) bool {
	switch status {
	case "completed":
		return true
	case "failed":
		return errorCode == "engine_failed" && slices.Contains(providerErrorCodes, ProviderErrorCode(engineErrorCode))
	default:
		return false
	}
}
