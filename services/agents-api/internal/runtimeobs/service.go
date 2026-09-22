package runtimeobs

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"
)

var providerTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

type Observation struct {
	Target         Target
	Status         Status
	Sample         *Sample
	Reason         string
	ProviderType   string
	ResolvedAt     time.Time
	SourceDuration time.Duration
}

type Service struct {
	resolver TargetResolver
	sources  map[string]Source
	now      func() time.Time
	exports  *exportDispatcher
}

func NewService(resolver TargetResolver, sources map[string]Source, options ...ServiceOption) (*Service, error) {
	if resolver == nil {
		return nil, errors.New("Runtime observation resolver is required")
	}
	config := serviceOptions{}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("invalid Runtime observation service option")
		}
		if err := option(&config); err != nil {
			return nil, err
		}
	}
	copySources := make(map[string]Source, len(sources))
	for key, source := range sources {
		if key == "" || source == nil {
			return nil, errors.New("invalid Runtime observation source")
		}
		copySources[key] = source
	}
	service := &Service{resolver: resolver, sources: copySources, now: time.Now}
	if config.exporter != nil {
		service.exports = newExportDispatcher(config.exporter, config.exportOptions)
	}
	return service, nil
}

// Close drains pending history handoffs within ctx. Current-observation callers
// may keep using a Service without an exporter; Close is then a no-op.
func (s *Service) Close(ctx context.Context) error {
	if s.exports == nil {
		return nil
	}
	return s.exports.close(ctx)
}

func (s *Service) finish(observation Observation) Observation {
	if s.exports != nil {
		s.exports.enqueue(exportRecord(observation))
	}
	return observation
}

func (s *Service) ObserveSession(ctx context.Context, tenantID, sessionID string) (Observation, error) {
	target, err := s.resolver.Resolve(ctx, tenantID, sessionID)
	resolvedAt := s.now()
	if errors.Is(err, ErrUnavailable) {
		if target.TenantID != tenantID || target.SessionID != sessionID || target.Mode != ModeManaged || target.EnvironmentID == "" {
			return Observation{}, errors.New("Runtime observation resolver returned invalid pending allocation identity")
		}
		return s.finish(Observation{Target: target, Status: StatusUnavailable, Reason: "allocation_pending", ResolvedAt: resolvedAt}), nil
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
		return s.finish(Observation{Target: target, Status: StatusUnsupported, Reason: "runtime_mode_not_observable", ResolvedAt: resolvedAt}), nil
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
		return s.finish(Observation{Target: target, Status: StatusUnavailable, Reason: "allocation_pending", ResolvedAt: resolvedAt}), nil
	case "cleanup_pending", "released":
		return s.finish(Observation{Target: target, Status: StatusUnavailable, Reason: "runtime_not_running", ResolvedAt: resolvedAt}), nil
	case "running":
	default:
		return Observation{}, errors.New("invalid managed Runtime allocation state")
	}
	source, ok := s.sources[target.Instance.ProviderKey]
	if !ok {
		return s.finish(Observation{Target: target, Status: StatusUnavailable, Reason: "source_not_configured", ResolvedAt: resolvedAt}), nil
	}
	providerType := ""
	if typed, ok := source.(interface{ ObservationProviderType() string }); ok {
		providerType = typed.ObservationProviderType()
		if providerType != "" && !providerTypePattern.MatchString(providerType) {
			return Observation{}, errors.New("invalid Runtime observation provider type")
		}
	}
	sourceStarted := time.Now()
	sample, err := source.Observe(ctx, target)
	sourceDuration := time.Since(sourceStarted)
	if errors.Is(err, context.DeadlineExceeded) {
		return s.finish(Observation{Target: target, Status: StatusUnavailable, Reason: "sample_timeout", ProviderType: providerType, ResolvedAt: s.now(), SourceDuration: sourceDuration}), nil
	}
	if errors.Is(err, ErrNotRunning) {
		return s.finish(Observation{Target: target, Status: StatusUnavailable, Reason: "runtime_not_running", ProviderType: providerType, ResolvedAt: s.now(), SourceDuration: sourceDuration}), nil
	}
	if errors.Is(err, ErrUnavailable) {
		return s.finish(Observation{Target: target, Status: StatusUnavailable, Reason: "sample_unavailable", ProviderType: providerType, ResolvedAt: s.now(), SourceDuration: sourceDuration}), nil
	}
	if err != nil {
		return Observation{}, fmt.Errorf("observe Runtime: %w", err)
	}
	if err := sample.validate(s.now()); err != nil {
		return Observation{}, err
	}
	return s.finish(Observation{Target: target, Status: StatusObserved, Sample: &sample, ProviderType: providerType, ResolvedAt: s.now(), SourceDuration: sourceDuration}), nil
}
