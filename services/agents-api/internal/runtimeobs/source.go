package runtimeobs

import "context"

// Source reads one provider-owned Runtime instance. Implementations must verify
// ownership before returning data and must not renew, restart, or stop compute.
type Source interface {
	Observe(context.Context, Target) (Sample, error)
}

type TargetResolver interface {
	Resolve(context.Context, string, string) (Target, error)
}
