package modelconfiguration

import "slices"

// Observation names a root Turn whose committed outcome may update the
// last-use observations of the deployment default its Session froze.
type Observation struct {
	TenantID, SessionID, TurnID string
}

// providerErrorCodes are the engine failures that describe the model provider
// itself. Context-length and cyber-policy failures describe the request. The
// observation statement's fence lists the same codes; testdata holds the cases
// both are checked against.
var providerErrorCodes = []string{
	"authentication_error", "connection_failed", "rate_limit_exceeded",
	"usage_limit_exceeded", "server_overloaded", "server_error",
	"resource_not_found", "request_timeout", "invalid_request",
}

// ShouldObserveProvider reports whether a committed root Turn outcome says
// something about its model provider: every completed Turn, and a failed Turn
// whose engine reported a provider error. It only spares a database round trip;
// the observation statement re-checks the committed outcome.
func ShouldObserveProvider(status, errorCode, engineErrorCode string) bool {
	switch status {
	case "completed":
		return true
	case "failed":
		return errorCode == "engine_failed" && slices.Contains(providerErrorCodes, engineErrorCode)
	default:
		return false
	}
}
