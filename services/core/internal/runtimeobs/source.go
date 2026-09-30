package runtimeobs

import (
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
)

var (
	ErrUnavailable = errors.New("Runtime observation unavailable")
	ErrNotRunning  = errors.New("Runtime is not running")
)

// Source reads one provider-owned Runtime instance. Implementations must verify
// ownership before returning data and must not renew, restart, or stop compute.
type Source interface {
	providercontract.Declared
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
