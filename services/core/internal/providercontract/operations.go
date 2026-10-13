// Package providercontract describes explicit resource-provider operations.
// It does not describe Harness capabilities or external API optional fields.
package providercontract

import (
	"errors"
	"fmt"
	"regexp"
)

var ErrUnsupported = errors.New("provider operation unsupported")
var ErrContract = errors.New("invalid provider operation declaration")
var reasonPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,95}$`)

type Support struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

const Supported = "supported"
const Unsupported = "unsupported"

// Zero values are omissions, never an unsupported declaration.
type Operations map[string]Support
type Declared interface{ ProviderOperations() Operations }

// UnsupportedError contains only an operation name and an authored safe code.
// It never carries a native error, credential, endpoint or resource identifier.
type UnsupportedError struct {
	Operation string `json:"operation"`
	Reason    string `json:"reason"`
}

func (e *UnsupportedError) Error() string {
	return "provider operation " + e.Operation + " unsupported: " + e.Reason
}
func (e *UnsupportedError) Unwrap() error { return ErrUnsupported }

func (s Support) Check(operation string) error {
	switch {
	case s.State == Supported && s.Reason == "":
		return nil
	case s.State == Unsupported && reasonPattern.MatchString(s.Reason):
		return &UnsupportedError{Operation: operation, Reason: s.Reason}
	default:
		return fmt.Errorf("%w: %s", ErrContract, operation)
	}
}

// UnsupportedReason verifies the operation and safe code before callers fall back
// or publish the reason. A bare sentinel is a contract violation.
func UnsupportedReason(err error, operation string) (string, bool) {
	var value *UnsupportedError
	if !errors.As(err, &value) || value.Operation != operation || !reasonPattern.MatchString(value.Reason) {
		return "", false
	}
	return value.Reason, true
}

func Require(p Declared, operation string) error {
	if p == nil {
		return ErrContract
	}
	return p.ProviderOperations()[operation].Check(operation)
}
