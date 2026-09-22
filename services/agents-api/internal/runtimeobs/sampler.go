package runtimeobs

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	defaultSamplerPageSize       = 32
	defaultSamplerConcurrency    = 8
	defaultSamplerSourceTimeout  = 2 * time.Second
	samplerOwnershipPollInterval = 100 * time.Millisecond
	historyOwnershipCheckTimeout = 250 * time.Millisecond
)

type SessionIdentity struct {
	TenantID, SessionID string
}

type SessionPage struct {
	Sessions   []SessionIdentity
	NextCursor string
}

type SessionLister interface {
	ListRuntimeObservationSessions(context.Context, string, int) (SessionPage, error)
}

type HistoryObserver interface {
	ObserveSessionForHistory(context.Context, string, string, OwnershipChecker, time.Duration) (Observation, error)
}

type OwnershipChecker interface {
	CheckOwnership(context.Context) error
}

type SamplerOptions struct {
	Interval      time.Duration
	PageSize      int
	Concurrency   int
	SourceTimeout time.Duration
	Report        func(SweepResult)
}

// SweepResult is deliberately low-cardinality. It reports collection coverage
// without exposing tenant, Session, allocation, provider-native, or error text.
type SweepResult struct {
	StartedAt, CompletedAt   time.Time
	Listed, Observed, Failed int
	Complete                 bool
}

type Sampler struct {
	lister     SessionLister
	observer   HistoryObserver
	owner      OwnershipChecker
	options    SamplerOptions
	now        func() time.Time
	afterSweep func(SweepResult)
}

func NewSampler(lister SessionLister, observer HistoryObserver, owner OwnershipChecker, options SamplerOptions) (*Sampler, error) {
	if lister == nil || observer == nil || owner == nil {
		return nil, errors.New("Runtime history sampler dependencies are required")
	}
	if options.Interval <= 0 {
		return nil, errors.New("Runtime history sampler interval must be positive")
	}
	if options.PageSize == 0 {
		options.PageSize = defaultSamplerPageSize
	}
	if options.PageSize < 1 || options.PageSize > 100 {
		return nil, errors.New("Runtime history sampler page size must be 1..100")
	}
	if options.Concurrency == 0 {
		options.Concurrency = defaultSamplerConcurrency
	}
	if options.Concurrency < 1 || options.Concurrency > 32 {
		return nil, errors.New("Runtime history sampler concurrency must be 1..32")
	}
	if options.SourceTimeout == 0 {
		options.SourceTimeout = defaultSamplerSourceTimeout
	}
	if options.SourceTimeout < time.Millisecond || options.SourceTimeout > 30*time.Second {
		return nil, errors.New("Runtime history sampler source timeout is out of range")
	}
	return &Sampler{lister: lister, observer: observer, owner: owner, options: options, now: time.Now, afterSweep: options.Report}, nil
}

// Run performs one immediate full keyset sweep and then repeats without overlap.
// A failed sweep is isolated from execution and retried at the next interval.
func (s *Sampler) Run(ctx context.Context) error {
	for {
		result := s.sweep(ctx)
		s.report(result)
		if err := ctx.Err(); err != nil {
			return err
		}
		timer := time.NewTimer(s.options.Interval)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

func (s *Sampler) report(result SweepResult) {
	if s.afterSweep == nil {
		return
	}
	defer func() { _ = recover() }()
	s.afterSweep(result)
}

func (s *Sampler) sweep(ctx context.Context) (result SweepResult) {
	result.StartedAt = s.now()
	defer func() { result.CompletedAt = s.now() }()
	sweepCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := s.checkOwnership(sweepCtx); err != nil {
		return result
	}
	go s.watchOwnership(sweepCtx, cancel)
	cursor := ""
	for {
		if err := s.checkOwnership(sweepCtx); err != nil {
			return result
		}
		page, err := s.lister.ListRuntimeObservationSessions(sweepCtx, cursor, s.options.PageSize)
		if err != nil || len(page.Sessions) > s.options.PageSize ||
			(page.NextCursor != "" && (len(page.Sessions) == 0 || page.NextCursor == cursor || page.NextCursor != page.Sessions[len(page.Sessions)-1].SessionID)) {
			return result
		}
		result.Listed += len(page.Sessions)
		observed, failed := s.samplePage(sweepCtx, page.Sessions)
		result.Observed += observed
		result.Failed += failed
		if sweepCtx.Err() != nil {
			return result
		}
		if page.NextCursor == "" {
			result.Complete = true
			return result
		}
		cursor = page.NextCursor
	}
}

func (s *Sampler) watchOwnership(ctx context.Context, cancel context.CancelFunc) {
	ticker := time.NewTicker(samplerOwnershipPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := s.checkOwnership(ctx); err != nil {
				cancel()
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Sampler) checkOwnership(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, historyOwnershipCheckTimeout)
	defer cancel()
	return s.owner.CheckOwnership(checkCtx)
}

func (s *Sampler) samplePage(ctx context.Context, sessions []SessionIdentity) (int, int) {
	semaphore := make(chan struct{}, s.options.Concurrency)
	var wait sync.WaitGroup
	var mu sync.Mutex
	observed, failed := 0, 0
	for _, session := range sessions {
		if session.TenantID == "" || session.SessionID == "" {
			failed++
			continue
		}
		wait.Add(1)
		go func(session SessionIdentity) {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}
			if err := s.checkOwnership(ctx); err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}
			_, err := s.observer.ObserveSessionForHistory(ctx, session.TenantID, session.SessionID, s.owner, s.options.SourceTimeout)
			mu.Lock()
			if err == nil {
				observed++
			} else {
				failed++
			}
			mu.Unlock()
		}(session)
	}
	wait.Wait()
	return observed, failed
}
