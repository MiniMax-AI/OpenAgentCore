package runtimeobs

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/providercontract"
)

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
	sources  map[string]SourceResolver
	now      func() time.Time
	exports  []*exportDispatcher
}

func NewService(resolver TargetResolver, sources map[string]SourceResolver, options ...ServiceOption) (*Service, error) {
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
	copySources := make(map[string]SourceResolver, len(sources))
	for key, source := range sources {
		if key == "" || source == nil {
			return nil, errors.New("invalid Runtime observation source")
		}
		if err := providercontract.Validate(source, reflect.TypeFor[SourceResolver]()); err != nil {
			return nil, err
		}
		if err := providercontract.Require(source, "ResolveObservationSource"); err != nil {
			return nil, fmt.Errorf("%w: observation source resolution must be supported", providercontract.ErrContract)
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

// PageOptions bounds one page of current reads.
type PageOptions struct {
	// Concurrency bounds identity resolution and per-target provider reads.
	Concurrency int
	// SourceTimeout bounds each provider read, including one batch read.
	SourceTimeout time.Duration
}

// ObserveSessions observes one page of Sessions. Running targets of a source
// that explicitly supports BatchSource share one provider read per MaxBatchTargets;
// other sources are read per target, exactly as ObserveSession reads them.
// Results and errors are aligned with sessions.
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
	key          string
	source       Source
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
	// A provider key selects one source; keep page order within each group.
	var keys []string
	groups := map[string][]*sourceRead{}
	for _, read := range reads {
		if read == nil {
			continue
		}
		if _, ok := groups[read.key]; !ok {
			keys = append(keys, read.key)
		}
		groups[read.key] = append(groups[read.key], read)
	}
	for _, key := range keys {
		group := groups[key]
		sourceCtx, stop := sourceContext(ctx, options.SourceTimeout)
		source, err := s.sources[key].ResolveObservationSource(sourceCtx)
		stop()
		if err == nil {
			err = ValidateSource(source)
		}
		if err != nil {
			for _, read := range group {
				observations[read.index], errs[read.index] = s.complete(ctx, read, Sample{}, err, 0, collectionSource, owner)
			}
			continue
		}
		providerType := source.ObservationProviderType()
		for _, read := range group {
			read.source, read.providerType = source, providerType
		}
		for start := 0; start < len(group); start += MaxBatchTargets {
			chunk := group[start:min(start+MaxBatchTargets, len(group))]
			if s.readBatch(ctx, chunk, observations, errs, collectionSource, owner, options.SourceTimeout) {
				continue
			}
			parallel(len(chunk), options.Concurrency, func(index int) {
				read := chunk[index]
				sourceCtx, stop := sourceContext(ctx, options.SourceTimeout)
				started := time.Now()
				var sample Sample
				err := providercontract.Require(read.source, "Observe")
				if err == nil {
					sample, err = read.source.Observe(sourceCtx, read.target)
				}
				stop()
				observations[read.index], errs[read.index] = s.complete(ctx, read, sample, err, time.Since(started), collectionSource, owner)
			})
		}
	}
	return observations, errs
}

// readBatch reports false, without results, when the source has no batch read
// for its current provider.
func (s *Service) readBatch(ctx context.Context, chunk []*sourceRead, observations []Observation, errs []error, collectionSource CollectionSource, owner OwnershipChecker, sourceTimeout time.Duration) bool {
	batch, ok := chunk[0].source.(BatchSource)
	if !ok { // NewService rejects this; never treat malformed registration as unsupported.
		for _, read := range chunk {
			errs[read.index] = providercontract.ErrContract
		}
		return true
	}
	if err := providercontract.Require(chunk[0].source, "ObserveBatch"); err != nil {
		if errors.Is(err, providercontract.ErrUnsupported) {
			return false
		}
		for _, read := range chunk {
			errs[read.index] = err
		}
		return true
	}
	targets := make([]Target, len(chunk))
	for index, read := range chunk {
		targets[index] = read.target
	}
	// One batch read replaces up to MaxBatchTargets single reads, so it may take
	// longer than one of them without exceeding the page's overall cost.
	if sourceTimeout > 0 {
		sourceTimeout = max(sourceTimeout, minBatchSourceTimeout)
	}
	sourceCtx, stop := sourceContext(ctx, sourceTimeout)
	started := time.Now()
	results, batchErr := batch.ObserveBatch(sourceCtx, targets)
	stop()
	if _, unsupported := providercontract.UnsupportedReason(batchErr, "ObserveBatch"); unsupported {
		return false
	}
	if errors.Is(batchErr, providercontract.ErrUnsupported) {
		batchErr = providercontract.ErrContract
	}
	if batchErr != nil {
		for _, read := range chunk {
			observations[read.index], errs[read.index] = s.complete(ctx, read, Sample{}, batchErr, 0, collectionSource, owner)
		}
		return true
	}
	duration := time.Since(started)
	for index, read := range chunk {
		if len(results) != len(chunk) {
			errs[read.index] = errors.New("Runtime observation batch returned mismatched results")
			continue
		}
		// The rows share one provider read; only the first carries its duration,
		// so sample-duration telemetry counts each read once.
		rowDuration := time.Duration(0)
		if index == 0 {
			rowDuration = duration
		}
		observations[read.index], errs[read.index] = s.complete(ctx, read, results[index].Sample, results[index].Err, rowDuration, collectionSource, owner)
	}
	return true
}

// resolve returns either a finished observation or a pending provider read.
func (s *Service) resolve(ctx context.Context, tenantID, sessionID string, owner OwnershipChecker) (Observation, *sourceRead, error) {
	if err := checkHistoryOwnership(ctx, owner); err != nil {
		return Observation{}, nil, err
	}
	target, err := s.resolver.Resolve(ctx, tenantID, sessionID)
	resolvedAt := s.now()
	if errors.Is(err, ErrUnavailable) {
		if target.TenantID != tenantID || target.SessionID != sessionID || target.Mode != ModeManaged || target.EnvironmentID == "" {
			return Observation{}, nil, errors.New("Runtime observation resolver returned invalid pending allocation identity")
		}
		return Observation{Target: target, Status: StatusUnavailable, Reason: "allocation_pending", ResolvedAt: resolvedAt}, nil, nil
	}
	if err != nil {
		return Observation{}, nil, err
	}
	if target.TenantID != tenantID || target.SessionID != sessionID {
		return Observation{}, nil, errors.New("Runtime observation resolver returned mismatched ownership")
	}
	if (target.Mode == ModeNone && target.EnvironmentID != "") ||
		((target.Mode == ModeSelfHosted || target.Mode == ModeManaged) && target.EnvironmentID == "") {
		return Observation{}, nil, errors.New("Runtime observation resolver returned mismatched Environment identity")
	}
	if target.Mode == ModeNone || target.Mode == ModeSelfHosted {
		return Observation{Target: target, Status: StatusUnsupported, Reason: "runtime_mode_not_observable", ResolvedAt: resolvedAt}, nil, nil
	}
	if target.Mode != ModeManaged || target.Instance.AllocationID == "" || target.Instance.ProviderKey == "" {
		return Observation{}, nil, errors.New("invalid managed Runtime observation target")
	}
	if !target.Instance.AllocationCreatedAt.IsZero() &&
		(target.Instance.AllocationCreatedAt.Unix() < 0 || target.Instance.AllocationCreatedAt.After(resolvedAt)) {
		return Observation{}, nil, errors.New("invalid managed Runtime allocation creation time")
	}
	switch target.Instance.AllocationState {
	case "creating":
		return Observation{Target: target, Status: StatusUnavailable, Reason: "allocation_pending", ResolvedAt: resolvedAt}, nil, nil
	case "cleanup_pending", "released":
		return Observation{Target: target, Status: StatusUnavailable, Reason: "runtime_not_running", ResolvedAt: resolvedAt}, nil, nil
	case "running":
	default:
		return Observation{}, nil, errors.New("invalid managed Runtime allocation state")
	}
	_, ok := s.sources[target.Instance.ProviderKey]
	if !ok {
		return Observation{Target: target, Status: StatusUnavailable, Reason: "source_not_configured", ResolvedAt: resolvedAt}, nil, nil
	}
	return Observation{}, &sourceRead{key: target.Instance.ProviderKey, target: target}, nil
}

// complete classifies one provider result and hands it to history export.
func (s *Service) complete(ctx context.Context, read *sourceRead, sample Sample, err error, sourceDuration time.Duration, collectionSource CollectionSource, owner OwnershipChecker) (Observation, error) {
	observation := Observation{Target: read.target, Status: StatusUnavailable, ProviderType: read.providerType, SourceDuration: sourceDuration}
	switch {
	case errors.Is(err, providercontract.ErrUnsupported):
		reason, valid := providercontract.UnsupportedReason(err, "Observe")
		if !valid {
			return Observation{}, providercontract.ErrContract
		}
		observation.Status, observation.Reason = StatusUnsupported, reason
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
		observation.Status, observation.Sample = StatusObserved, &sample
	}
	observation.ResolvedAt = s.now()
	return s.finish(ctx, observation, collectionSource, owner)
}

const minBatchSourceTimeout = 5 * time.Second

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
