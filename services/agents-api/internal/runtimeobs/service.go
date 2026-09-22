package runtimeobs

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Observation struct {
	Target Target
	Status Status
	Sample *Sample
	Reason string
}

type Service struct {
	resolver TargetResolver
	sources  map[string]Source
	now      func() time.Time
}

func NewService(resolver TargetResolver, sources map[string]Source) (*Service, error) {
	if resolver == nil {
		return nil, errors.New("Runtime observation resolver is required")
	}
	copySources := make(map[string]Source, len(sources))
	for key, source := range sources {
		if key == "" || source == nil {
			return nil, errors.New("invalid Runtime observation source")
		}
		copySources[key] = source
	}
	return &Service{resolver: resolver, sources: copySources, now: time.Now}, nil
}

func (s *Service) ObserveSession(ctx context.Context, tenantID, sessionID string) (Observation, error) {
	target, err := s.resolver.Resolve(ctx, tenantID, sessionID)
	if errors.Is(err, ErrUnavailable) {
		return Observation{Target: target, Status: StatusUnavailable, Reason: "runtime_allocation_unavailable"}, nil
	}
	if err != nil {
		return Observation{}, err
	}
	if target.Mode == ModeNone || target.Mode == ModeSelfHosted {
		return Observation{Target: target, Status: StatusUnsupported, Reason: "runtime_mode_not_observable"}, nil
	}
	if target.Mode != ModeManaged || target.Instance.AllocationID == "" || target.Instance.ProviderKey == "" {
		return Observation{}, errors.New("invalid managed Runtime observation target")
	}
	source, ok := s.sources[target.Instance.ProviderKey]
	if !ok {
		return Observation{Target: target, Status: StatusUnavailable, Reason: "runtime_source_unavailable"}, nil
	}
	sample, err := source.Observe(ctx, target)
	if errors.Is(err, ErrUnavailable) {
		return Observation{Target: target, Status: StatusUnavailable, Reason: "runtime_sample_unavailable"}, nil
	}
	if err != nil {
		return Observation{}, fmt.Errorf("observe Runtime: %w", err)
	}
	if err := sample.validate(s.now()); err != nil {
		return Observation{}, err
	}
	return Observation{Target: target, Status: StatusObserved, Sample: &sample}, nil
}
