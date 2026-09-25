package runtimeobs

import (
	"context"
	"errors"
)

var (
	ErrUnavailable = errors.New("Runtime observation unavailable")
	ErrNotRunning  = errors.New("Runtime is not running")
)

// Source reads one provider-owned Runtime instance. Implementations must verify
// ownership before returning data and must not renew, restart, or stop compute.
type Source interface {
	Observe(context.Context, Target) (Sample, error)
}

// MaxBatchTargets bounds the targets of one BatchSource read.
const MaxBatchTargets = 100

// BatchSource is an optional Source extension for providers that read many
// Runtime instances in one bounded request. ObserveBatch returns ok=false,
// without reading, when the current provider has no batch read; the Service
// then reads each target with Observe. Otherwise it returns one result per
// target, in order, under the same ownership rules as Observe.
type BatchSource interface {
	ObserveBatch(context.Context, []Target) (results []BatchResult, ok bool)
}

type BatchResult struct {
	Sample Sample
	Err    error
}

type TargetResolver interface {
	Resolve(context.Context, string, string) (Target, error)
}
