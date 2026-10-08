package runtimeobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
)

type Observation struct {
	Target         Target
	Status         v1.RuntimeObservationStatus
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
	source   func(context.Context) (Source, string, error)
	now      func() time.Time
	exports  []*exportDispatcher
}

// NewService reads managed Runtimes through source, which returns the Sandbox
// Provider of the deployment's current selection and its registered kind, or
// ErrUnavailable while none is selected.
func NewService(resolver TargetResolver, source func(context.Context) (Source, string, error), options ...ServiceOption) (*Service, error) {
	if resolver == nil || source == nil {
		return nil, errors.New("Runtime observation resolver and source are required")
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
	service := &Service{resolver: resolver, source: source, now: time.Now}
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

// PageOptions bounds one page of current reads.
type PageOptions struct {
	// Concurrency bounds identity resolution and per-target provider reads.
	Concurrency int
	// SourceTimeout bounds each provider read.
	SourceTimeout time.Duration
}

// ObserveSessions observes one page of Sessions, reading each running target
// exactly as ObserveSession reads it. Results and errors are aligned with sessions.
func (s *Service) ObserveSessions(ctx context.Context, sessions []SessionIdentity, options PageOptions) ([]Observation, []error) {
	return s.observeSessions(ctx, sessions, CollectionSourceOnRead, nil, options)
}

// ObserveSessionsForHistory is the periodic-collection form of ObserveSessions.
func (s *Service) ObserveSessionsForHistory(ctx context.Context, sessions []SessionIdentity, owner OwnershipChecker, options PageOptions) ([]Observation, []error) {
	if owner == nil || options.SourceTimeout <= 0 || options.SourceTimeout > 30*time.Second {
		errs := make([]error, len(sessions))
		for index := range errs {
			errs[index] = errors.New("invalid Runtime history page collection")
		}
		return make([]Observation, len(sessions)), errs
	}
	return s.observeSessions(ctx, sessions, CollectionSourcePeriodic, owner, options)
}

func (s *Service) observeSession(ctx context.Context, tenantID, sessionID string, collectionSource CollectionSource, owner OwnershipChecker, sourceTimeout time.Duration) (Observation, error) {
	observations, errs := s.observeSessions(ctx, []SessionIdentity{{TenantID: tenantID, SessionID: sessionID}}, collectionSource, owner, PageOptions{Concurrency: 1, SourceTimeout: sourceTimeout})
	return observations[0], errs[0]
}

// sourceRead is a resolved running managed target awaiting its provider sample.
type sourceRead struct {
	index        int
	target       Target
	providerType string
}

func (s *Service) observeSessions(ctx context.Context, sessions []SessionIdentity, collectionSource CollectionSource, owner OwnershipChecker, options PageOptions) ([]Observation, []error) {
	observations := make([]Observation, len(sessions))
	errs := make([]error, len(sessions))
	reads := make([]*sourceRead, len(sessions))
	parallel(len(sessions), options.Concurrency, func(index int) {
		observation, read, err := s.resolve(ctx, sessions[index].TenantID, sessions[index].SessionID, owner)
		if err == nil && read == nil {
			observation, err = s.finish(ctx, observation, collectionSource, owner)
		}
		if read != nil {
			read.index = index
			reads[index] = read
		}
		observations[index], errs[index] = observation, err
	})
	var pending []*sourceRead
	for _, read := range reads {
		if read != nil {
			pending = append(pending, read)
		}
	}
	if len(pending) == 0 {
		return observations, errs
	}
	// One source serves the whole page.
	sourceCtx, stop := sourceContext(ctx, options.SourceTimeout)
	source, providerType, err := s.source(sourceCtx)
	stop()
	if err != nil {
		for _, read := range pending {
			observations[read.index], errs[read.index] = s.complete(ctx, read, Sample{}, err, 0, collectionSource, owner)
		}
		return observations, errs
	}
	parallel(len(pending), options.Concurrency, func(index int) {
		read := pending[index]
		read.providerType = providerType
		sourceCtx, stop := sourceContext(ctx, options.SourceTimeout)
		started := time.Now()
		var sample Sample
		err := providercontract.Require(source, "Observe")
		if err == nil {
			sample, err = source.Observe(sourceCtx, read.target)
		}
		stop()
		observations[read.index], errs[read.index] = s.complete(ctx, read, sample, err, time.Since(started), collectionSource, owner)
	})
	return observations, errs
}

// resolve returns either a finished observation or a pending provider read.
func (s *Service) resolve(ctx context.Context, tenantID, sessionID string, owner OwnershipChecker) (Observation, *sourceRead, error) {
	if err := checkHistoryOwnership(ctx, owner); err != nil {
		return Observation{}, nil, err
	}
	target, err := s.resolver.Resolve(ctx, tenantID, sessionID)
	resolvedAt := s.now()
	if errors.Is(err, ErrUnavailable) {
		if target.TenantID != tenantID || target.SessionID != sessionID || target.Mode != v1.RuntimeModeManaged || target.EnvironmentID == "" {
			return Observation{}, nil, errors.New("Runtime observation resolver returned invalid pending allocation identity")
		}
		return Observation{Target: target, Status: v1.RuntimeStatusUnavailable, Reason: "allocation_pending", ResolvedAt: resolvedAt}, nil, nil
	}
	if err != nil {
		return Observation{}, nil, err
	}
	if target.TenantID != tenantID || target.SessionID != sessionID {
		return Observation{}, nil, errors.New("Runtime observation resolver returned mismatched ownership")
	}
	if (target.Mode == v1.RuntimeModeNone && target.EnvironmentID != "") ||
		((target.Mode == v1.RuntimeModeSelfHosted || target.Mode == v1.RuntimeModeManaged) && target.EnvironmentID == "") {
		return Observation{}, nil, errors.New("Runtime observation resolver returned mismatched Environment identity")
	}
	if target.Mode == v1.RuntimeModeNone || target.Mode == v1.RuntimeModeSelfHosted {
		return Observation{Target: target, Status: v1.RuntimeStatusUnsupported, Reason: "runtime_mode_not_observable", ResolvedAt: resolvedAt}, nil, nil
	}
	if target.Mode != v1.RuntimeModeManaged || target.Instance.AllocationID == "" || target.Instance.ProviderKey == "" {
		return Observation{}, nil, errors.New("invalid managed Runtime observation target")
	}
	if !target.Instance.AllocationCreatedAt.IsZero() &&
		(target.Instance.AllocationCreatedAt.Unix() < 0 || target.Instance.AllocationCreatedAt.After(resolvedAt)) {
		return Observation{}, nil, errors.New("invalid managed Runtime allocation creation time")
	}
	switch target.Instance.AllocationState {
	case "creating":
		return Observation{Target: target, Status: v1.RuntimeStatusUnavailable, Reason: "allocation_pending", ResolvedAt: resolvedAt}, nil, nil
	case "cleanup_pending", "released":
		return Observation{Target: target, Status: v1.RuntimeStatusUnavailable, Reason: "runtime_not_running", ResolvedAt: resolvedAt}, nil, nil
	case "running":
	default:
		return Observation{}, nil, errors.New("invalid managed Runtime allocation state")
	}
	return Observation{}, &sourceRead{target: target}, nil
}

// complete classifies one provider result and hands it to history export.
func (s *Service) complete(ctx context.Context, read *sourceRead, sample Sample, err error, sourceDuration time.Duration, collectionSource CollectionSource, owner OwnershipChecker) (Observation, error) {
	observation := Observation{Target: read.target, Status: v1.RuntimeStatusUnavailable, ProviderType: read.providerType, SourceDuration: sourceDuration}
	switch {
	case errors.Is(err, providercontract.ErrUnsupported):
		reason, valid := providercontract.UnsupportedReason(err, "Observe")
		if !valid {
			return Observation{}, providercontract.ErrContract
		}
		observation.Status, observation.Reason = v1.RuntimeStatusUnsupported, reason
	case errors.Is(err, context.DeadlineExceeded):
		observation.Reason = "sample_timeout"
	case errors.Is(err, ErrNotRunning):
		observation.Reason = "runtime_not_running"
	case errors.Is(err, ErrUnavailable):
		observation.Reason = "sample_unavailable"
	case err != nil:
		return Observation{}, fmt.Errorf("observe Runtime: %w", err)
	default:
		if err := sample.validate(s.now()); err != nil {
			return Observation{}, err
		}
		observation.Status, observation.Sample = v1.RuntimeStatusObserved, &sample
	}
	observation.ResolvedAt = s.now()
	return s.finish(ctx, observation, collectionSource, owner)
}

func sourceContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return ctx, func() {}
}

// parallel runs work for every index with at most limit concurrent calls.
func parallel(count, limit int, work func(int)) {
	if count == 1 || limit <= 1 {
		for index := range count {
			work(index)
		}
		return
	}
	semaphore := make(chan struct{}, limit)
	var wait sync.WaitGroup
	for index := range count {
		wait.Add(1)
		semaphore <- struct{}{}
		go func() {
			defer func() { <-semaphore; wait.Done() }()
			work(index)
		}()
	}
	wait.Wait()
}

func checkHistoryOwnership(ctx context.Context, owner OwnershipChecker) error {
	if owner == nil {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, historyOwnershipCheckTimeout)
	defer cancel()
	return owner.CheckOwnership(checkCtx)
}
