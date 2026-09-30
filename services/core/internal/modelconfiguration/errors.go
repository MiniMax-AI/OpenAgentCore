package modelconfiguration

import "errors"

// Replace and Resolve report a configuration the Harness declaration rejects
// with the contract's *v1.ModelProviderError, which names the field, and a
// missing credential key with credentialcrypto.ErrUnavailable. A bundle that
// fails to open is an internal error. Storage passes textvalue.ErrUnstorable
// and adminaudit.ErrInvalidSource through unchanged.
var (
	// ErrNotFound reports a Harness without a deployment default.
	ErrNotFound = errors.New("the harness has no deployment default model configuration")
	// ErrInvalidObservation reports an Observation whose identifiers are not
	// nonzero UUIDs.
	ErrInvalidObservation = errors.New("an observation requires nonzero tenant, Session and Turn UUIDs")
)
