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

type CollectionSource string

const (
	CollectionSourceOnRead   CollectionSource = "on_read"
	CollectionSourcePeriodic CollectionSource = "periodic"
)

type Service struct {
	resolver TargetResolver
	sources  map[string]Source
	now      func() time.Time
	exports  []*exportDispatcher
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
	for _, export := range config.exporters {
		service.exports = append(service.exports, newExportDispatcher(export.exporter, export.exportOptions))
	}
	return service, nil
}

// Close drains pending history handoffs within ctx. Current-observation callers
// may keep using a Service without an exporter; Close is then a no-op.
func (s *Service) Close(ctx context.Context) error {
	var result error
	for _, exporter := range s.exports {
		result = errors.Join(result, exporter.close(ctx))
	}
	return result
}

func (s *Service) finish(ctx context.Context, observation Observation, source CollectionSource, owner OwnershipChecker) (Observation, error) {
	if err := checkHistoryOwnership(ctx, owner); err != nil {
		return Observation{}, err
	}
	for _, exporter := range s.exports {
		exporter.enqueue(exportRecord(observation, source))
	}
	return observation, nil
}

func (s *Service) ObserveSession(ctx context.Context, tenantID, sessionID string) (Observation, error) {
	return s.observeSession(ctx, tenantID, sessionID, CollectionSourceOnRead, nil, 0)
}

// ObserveSessionForHistory performs the same provider-neutral current read, but
// marks its export as deployment-periodic so coverage queries can distinguish it
// from user-triggered API reads. It has no lifecycle side effects.
func (s *Service) ObserveSessionForHistory(ctx context.Context, tenantID, sessionID string, owner OwnershipChecker, sourceTimeout time.Duration) (Observation, error) {
	if owner == nil {
		return Observation{}, errors.New("Runtime history observation ownership is required")
	}
	if sourceTimeout <= 0 || sourceTimeout > 30*time.Second {
		return Observation{}, errors.New("Runtime history source timeout is out of range")
	}
	return s.observeSession(ctx, tenantID, sessionID, CollectionSourcePeriodic, owner, sourceTimeout)
}

func (s *Service) observeSession(ctx context.Context, tenantID, sessionID string, collectionSource CollectionSource, owner OwnershipChecker, sourceTimeout time.Duration) (Observation, error) {
	if err := checkHistoryOwnership(ctx, owner); err != nil {
		return Observation{}, err
	}
	target, err := s.resolver.Resolve(ctx, tenantID, sessionID)
	resolvedAt := s.now()
	if errors.Is(err, ErrUnavailable) {
		if target.TenantID != tenantID || target.SessionID != sessionID || target.Mode != ModeManaged || target.EnvironmentID == "" {
			return Observation{}, errors.New("Runtime observation resolver returned invalid pending allocation identity")
		}
		return s.finish(ctx, Observation{Target: target, Status: StatusUnavailable, Reason: "allocation_pending", ResolvedAt: resolvedAt}, collectionSource, owner)
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
		return s.finish(ctx, Observation{Target: target, Status: StatusUnsupported, Reason: "runtime_mode_not_observable", ResolvedAt: resolvedAt}, collectionSource, owner)
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
		return s.finish(ctx, Observation{Target: target, Status: StatusUnavailable, Reason: "allocation_pending", ResolvedAt: resolvedAt}, collectionSource, owner)
	case "cleanup_pending", "released":
		return s.finish(ctx, Observation{Target: target, Status: StatusUnavailable, Reason: "runtime_not_running", ResolvedAt: resolvedAt}, collectionSource, owner)
	case "running":
	default:
		return Observation{}, errors.New("invalid managed Runtime allocation state")
	}
	source, ok := s.sources[target.Instance.ProviderKey]
	if !ok {
		return s.finish(ctx, Observation{Target: target, Status: StatusUnavailable, Reason: "source_not_configured", ResolvedAt: resolvedAt}, collectionSource, owner)
	}
	providerType := ""
	if typed, ok := source.(interface{ ObservationProviderType() string }); ok {
		providerType = typed.ObservationProviderType()
		if providerType != "" && !providerTypePattern.MatchString(providerType) {
			return Observation{}, errors.New("invalid Runtime observation provider type")
		}
	}
	sourceStarted := time.Now()
	sourceCtx := ctx
	stopSource := func() {}
	if sourceTimeout > 0 {
		sourceCtx, stopSource = context.WithTimeout(ctx, sourceTimeout)
	}
	sample, err := source.Observe(sourceCtx, target)
	stopSource()
	sourceDuration := time.Since(sourceStarted)
	if errors.Is(err, context.DeadlineExceeded) {
		return s.finish(ctx, Observation{Target: target, Status: StatusUnavailable, Reason: "sample_timeout", ProviderType: providerType, ResolvedAt: s.now(), SourceDuration: sourceDuration}, collectionSource, owner)
	}
	if errors.Is(err, ErrNotRunning) {
		return s.finish(ctx, Observation{Target: target, Status: StatusUnavailable, Reason: "runtime_not_running", ProviderType: providerType, ResolvedAt: s.now(), SourceDuration: sourceDuration}, collectionSource, owner)
	}
	if errors.Is(err, ErrUnavailable) {
		return s.finish(ctx, Observation{Target: target, Status: StatusUnavailable, Reason: "sample_unavailable", ProviderType: providerType, ResolvedAt: s.now(), SourceDuration: sourceDuration}, collectionSource, owner)
	}
	if err != nil {
		return Observation{}, fmt.Errorf("observe Runtime: %w", err)
	}
	if err := sample.validate(s.now()); err != nil {
		return Observation{}, err
	}
	return s.finish(ctx, Observation{Target: target, Status: StatusObserved, Sample: &sample, ProviderType: providerType, ResolvedAt: s.now(), SourceDuration: sourceDuration}, collectionSource, owner)
}

func checkHistoryOwnership(ctx context.Context, owner OwnershipChecker) error {
	if owner == nil {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, historyOwnershipCheckTimeout)
	defer cancel()
	return owner.CheckOwnership(checkCtx)
}
