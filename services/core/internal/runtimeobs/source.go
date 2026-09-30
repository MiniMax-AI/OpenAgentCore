package runtimeobs

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
)

var (
	ErrUnavailable = errors.New("Runtime observation unavailable")
	ErrNotRunning  = errors.New("Runtime is not running")
)

// SourceResolver selects one immutable source for a page of provider reads.
// An unconfigured resolver returns ErrUnavailable. Registration never resolves
// a source; callers validate each selected source before reading it.
type SourceResolver interface {
	providercontract.Declared
	ResolveObservationSource(context.Context) (Source, error)
}

// Source reads one provider-owned Runtime instance. Implementations must verify
// ownership before returning data and must not renew, restart, or stop compute.
type Source interface {
	providercontract.Declared
	// ObservationProviderType is a nonempty, immutable telemetry identity.
	ObservationProviderType() string
	Observe(context.Context, Target) (Sample, error)
}

// MaxBatchTargets bounds the targets of one BatchSource read.
const MaxBatchTargets = 100

// BatchSource reads multiple instances under the same ownership rules as Observe.
// Unsupported returns a typed providercontract.UnsupportedError before reading;
// only that result permits per-target observation. Unavailable or failed reads
// must not silently retry through Observe. Successful results preserve target order.
type BatchSource interface {
	ObserveBatch(context.Context, []Target) (results []BatchResult, err error)
}

type BatchResult struct {
	Sample Sample
	Err    error
}

type TargetResolver interface {
	Resolve(context.Context, string, string) (Target, error)
}

var providerTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// ValidateSource checks every read operation and its immutable provider identity.
func ValidateSource(source Source) error {
	if err := providercontract.Validate(source, reflect.TypeFor[Source](), reflect.TypeFor[BatchSource]()); err != nil {
		return err
	}
	if err := providercontract.Require(source, "ObservationProviderType"); err != nil {
		return fmt.Errorf("%w: observation identity must be supported", providercontract.ErrContract)
	}
	if !providerTypePattern.MatchString(source.ObservationProviderType()) {
		return fmt.Errorf("%w: invalid Runtime observation provider type", providercontract.ErrContract)
	}
	return nil
}
