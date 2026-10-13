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

// Source is the observation half of a Sandbox Provider, which
// services/core/internal/sandbox/sandbox_provider.go defines.
type Source interface {
	providercontract.Declared
	Observe(context.Context, Target) (Sample, error)
}

type TargetResolver interface {
	Resolve(context.Context, string, string) (Target, error)
}
