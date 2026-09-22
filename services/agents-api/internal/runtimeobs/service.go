package runtimeobs

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Observation struct {
	Target       Target
	Status       Status
	Sample       *Sample
	Reason       string
	ProviderType string
	ResolvedAt   time.Time
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
	resolvedAt := s.now()
	if errors.Is(err, ErrUnavailable) {
		if target.TenantID != tenantID || target.SessionID != sessionID || target.Mode != ModeManaged || target.EnvironmentID == "" {
			return Observation{}, errors.New("Runtime observation resolver returned invalid pending allocation identity")
		}
		return Observation{Target: target, Status: StatusUnavailable, Reason: "allocation_pending", ResolvedAt: resolvedAt}, nil
	}
	if err != nil {
		return Observation{}, err
	}
	if target.TenantID != tenantID || target.SessionID != sessionID {
		return Observation{}, errors.New("Runtime observation resolver returned mismatched ownership")
	}
	if (target.Mode == ModeNone && target.EnvironmentID != "") ||
		((target.Mode == ModeSelfHosted || target.Mode == ModeManaged) && target.EnvironmentID == "") {
		return Observation{}, errors.New("Runtime observation resolver returned mismatched Environment identity")
	}
	if target.Mode == ModeNone || target.Mode == ModeSelfHosted {
		return Observation{Target: target, Status: StatusUnsupported, Reason: "runtime_mode_not_observable", ResolvedAt: resolvedAt}, nil
	}
	if target.Mode != ModeManaged || target.Instance.AllocationID == "" || target.Instance.ProviderKey == "" {
		return Observation{}, errors.New("invalid managed Runtime observation target")
	}
	if !target.Instance.AllocationCreatedAt.IsZero() &&
		(target.Instance.AllocationCreatedAt.Unix() < 0 || target.Instance.AllocationCreatedAt.After(resolvedAt)) {
		return Observation{}, errors.New("invalid managed Runtime allocation creation time")
	}
	switch target.Instance.AllocationState {
	case "creating":
		return Observation{Target: target, Status: StatusUnavailable, Reason: "allocation_pending", ResolvedAt: resolvedAt}, nil
	case "cleanup_pending", "released":
		return Observation{Target: target, Status: StatusUnavailable, Reason: "runtime_not_running", ResolvedAt: resolvedAt}, nil
	case "running":
	default:
		return Observation{}, errors.New("invalid managed Runtime allocation state")
	}
	source, ok := s.sources[target.Instance.ProviderKey]
	if !ok {
		return Observation{Target: target, Status: StatusUnavailable, Reason: "source_not_configured", ResolvedAt: resolvedAt}, nil
	}
	providerType := ""
	if typed, ok := source.(interface{ ObservationProviderType() string }); ok {
		providerType = typed.ObservationProviderType()
	}
	sample, err := source.Observe(ctx, target)
	if errors.Is(err, context.DeadlineExceeded) {
		return Observation{Target: target, Status: StatusUnavailable, Reason: "sample_timeout", ProviderType: providerType, ResolvedAt: s.now()}, nil
	}
	if errors.Is(err, ErrNotRunning) {
		return Observation{Target: target, Status: StatusUnavailable, Reason: "runtime_not_running", ProviderType: providerType, ResolvedAt: s.now()}, nil
	}
	if errors.Is(err, ErrUnavailable) {
		return Observation{Target: target, Status: StatusUnavailable, Reason: "sample_unavailable", ProviderType: providerType, ResolvedAt: s.now()}, nil
	}
	if err != nil {
		return Observation{}, fmt.Errorf("observe Runtime: %w", err)
	}
	if err := sample.validate(s.now()); err != nil {
		return Observation{}, err
	}
	return Observation{Target: target, Status: StatusObserved, Sample: &sample, ProviderType: providerType, ResolvedAt: s.now()}, nil
}
