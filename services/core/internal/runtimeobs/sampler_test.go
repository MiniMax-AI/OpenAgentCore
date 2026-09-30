package runtimeobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type samplerLister struct {
	mu      sync.Mutex
	pages   map[string]SessionPage
	cursors []string
}

type delayedSamplerResolver struct {
	*samplerLister
	delay  time.Duration
	target Target
}

func (r delayedSamplerResolver) Resolve(ctx context.Context, tenant, session string) (Target, error) {
	select {
	case <-time.After(r.delay):
	case <-ctx.Done():
		return Target{}, ctx.Err()
	}
	target := r.target
	target.TenantID = tenant
	target.SessionID = session
	return target, nil
}

func (l *samplerLister) ListRuntimeObservationSessions(_ context.Context, after string, _ int) (SessionPage, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cursors = append(l.cursors, after)
	return l.pages[after], nil
}

type samplerObserver struct {
	mu          sync.Mutex
	sessions    []SessionIdentity
	active, max int
	fail        map[string]bool
	wait        <-chan struct{}
}

func (o *samplerObserver) ObserveSessionsForHistory(ctx context.Context, sessions []SessionIdentity, _ OwnershipChecker, options PageOptions) ([]Observation, []error) {
	o.mu.Lock()
	o.active++
	if o.active > o.max {
		o.max = o.active
	}
	o.mu.Unlock()
	errs := make([]error, len(sessions))
	if o.wait != nil {
		sourceCtx, cancel := context.WithTimeout(ctx, options.SourceTimeout)
		defer cancel()
		select {
		case <-o.wait:
		case <-sourceCtx.Done():
			o.mu.Lock()
			o.active--
			o.mu.Unlock()
			for index := range errs {
				errs[index] = sourceCtx.Err()
			}
			return make([]Observation, len(sessions)), errs
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.active--
	for index, session := range sessions {
		o.sessions = append(o.sessions, session)
		if o.fail[session.SessionID] {
			errs[index] = errors.New("test failure")
		}
	}
	return make([]Observation, len(sessions)), errs
}

// countingSource blocks each per-target read until its source deadline.
type countingSource struct {
	mu          sync.Mutex
	active, max int
}

func (s *countingSource) Observe(ctx context.Context, _ Target) (Sample, error) {
	s.mu.Lock()
	s.active++
	s.max = max(s.max, s.active)
	s.mu.Unlock()
	<-ctx.Done()
	s.mu.Lock()
	s.active--
	s.mu.Unlock()
	return Sample{}, ctx.Err()
}

type samplerOwner struct{ err error }

func (o samplerOwner) CheckOwnership(context.Context) error { return o.err }

type sequenceOwner struct {
	calls     atomic.Int32
	failAfter int32
	lost      atomic.Bool
}

func (o *sequenceOwner) CheckOwnership(context.Context) error {
	call := o.calls.Add(1)
	if o.lost.Load() || (o.failAfter > 0 && call >= o.failAfter) {
		return errors.New("ownership lost")
	}
	return nil
}

func TestSamplerSweepsEveryPageAndIsolatesSessionFailures(t *testing.T) {
	lister := &samplerLister{pages: map[string]SessionPage{
		"":          {Sessions: []SessionIdentity{{TenantID: "tenant-a", SessionID: "session-a"}, {TenantID: "tenant-b", SessionID: "session-b"}}, NextCursor: "session-b"},
		"session-b": {Sessions: []SessionIdentity{{TenantID: "tenant-c", SessionID: "session-c"}}},
	}}
	observer := &samplerObserver{fail: map[string]bool{"session-b": true}}
	sampler, err := NewSampler(lister, observer, samplerOwner{}, SamplerOptions{Interval: time.Minute, PageSize: 2, Concurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	sampler.now = func() time.Time { now = now.Add(time.Second); return now }
	result := sampler.sweep(t.Context())
	if !result.Complete || result.Listed != 3 || result.Observed != 2 || result.Failed != 1 {
		t.Fatalf("unexpected sweep result: %+v", result)
	}
	if got := lister.cursors; len(got) != 2 || got[0] != "" || got[1] != "session-b" {
		t.Fatalf("unexpected keyset scan: %#v", got)
	}
	if len(observer.sessions) != 3 {
		t.Fatalf("not every Session was attempted: %#v", observer.sessions)
	}
}

func TestSamplerBoundsConcurrencyAndSourceDeadline(t *testing.T) {
	source := &countingSource{}
	target := Target{EnvironmentID: "environment", Mode: ModeManaged, Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"}}
	service, err := NewService(fixedResolver{target: target}, map[string]SourceResolver{"provider": source})
	if err != nil {
		t.Fatal(err)
	}
	lister := &samplerLister{pages: map[string]SessionPage{
		"": {Sessions: []SessionIdentity{
			{TenantID: "t", SessionID: "1"}, {TenantID: "t", SessionID: "2"}, {TenantID: "t", SessionID: "3"},
		}},
	}}
	sampler, err := NewSampler(lister, service, samplerOwner{}, SamplerOptions{Interval: time.Minute, Concurrency: 2, SourceTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	result := sampler.sweep(t.Context())
	// Source deadlines are recorded as sample_timeout observations.
	if !result.Complete || result.Observed != 3 || result.Failed != 0 || source.max != 2 {
		t.Fatalf("unexpected bounded result: result=%+v max=%d", result, source.max)
	}
}

func TestSamplerStopsBeforeListingWithoutOwnership(t *testing.T) {
	lister := &samplerLister{pages: map[string]SessionPage{}}
	sampler, err := NewSampler(lister, &samplerObserver{}, samplerOwner{err: errors.New("lost")}, SamplerOptions{Interval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	result := sampler.sweep(t.Context())
	if result.Complete || len(lister.cursors) != 0 {
		t.Fatalf("sampler ran without deployment ownership: %+v %#v", result, lister.cursors)
	}
}

func TestSamplerRejectsInvalidContinuationWithoutLooping(t *testing.T) {
	lister := &samplerLister{pages: map[string]SessionPage{"": {
		Sessions:   []SessionIdentity{{TenantID: "tenant", SessionID: "session"}},
		NextCursor: "different-session",
	}}}
	observer := &samplerObserver{}
	sampler, err := NewSampler(lister, observer, samplerOwner{}, SamplerOptions{Interval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	result := sampler.sweep(t.Context())
	if result.Complete || result.Listed != 0 || len(observer.sessions) != 0 || len(lister.cursors) != 1 {
		t.Fatalf("invalid continuation was accepted: result=%+v sessions=%#v cursors=%#v", result, observer.sessions, lister.cursors)
	}
}

func TestSamplerReportPanicIsIsolated(t *testing.T) {
	sampler, err := NewSampler(
		&samplerLister{pages: map[string]SessionPage{}},
		&samplerObserver{},
		samplerOwner{},
		SamplerOptions{Interval: time.Minute, Report: func(SweepResult) { panic("test") }},
	)
	if err != nil {
		t.Fatal(err)
	}
	sampler.report(SweepResult{Complete: true})
}

func TestSamplerRunDoesNotOverlapSweepsAndStops(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	observer := &samplerObserver{wait: release}
	lister := &samplerLister{pages: map[string]SessionPage{"": {Sessions: []SessionIdentity{{TenantID: "t", SessionID: "s"}}}}}
	sampler, err := NewSampler(lister, observer, samplerOwner{}, SamplerOptions{Interval: time.Millisecond, SourceTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	sampler.afterSweep = func(SweepResult) { started <- struct{}{} }
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- sampler.Run(ctx) }()
	deadline := time.After(time.Second)
	for {
		observer.mu.Lock()
		active := observer.active
		max := observer.max
		observer.mu.Unlock()
		if active == 1 {
			if max != 1 {
				t.Fatalf("overlapping sweep observed: max=%d", max)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("sampler did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected sampler exit: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("sampler did not stop")
	}
}

func TestSamplerCancelsProviderReadWhenOwnershipIsLost(t *testing.T) {
	release := make(chan struct{})
	observer := &samplerObserver{wait: release}
	owner := &sequenceOwner{}
	lister := &samplerLister{pages: map[string]SessionPage{"": {Sessions: []SessionIdentity{{TenantID: "t", SessionID: "s"}}}}}
	sampler, err := NewSampler(lister, observer, owner, SamplerOptions{Interval: time.Minute, SourceTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan SweepResult, 1)
	go func() { done <- sampler.sweep(t.Context()) }()
	deadline := time.After(time.Second)
	for {
		observer.mu.Lock()
		active := observer.active
		observer.mu.Unlock()
		if active == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("provider read did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	owner.lost.Store(true)
	select {
	case result := <-done:
		if result.Complete || result.Observed != 0 || result.Failed != 1 {
			t.Fatalf("lease-lost sweep was not fenced: %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("lease loss did not cancel provider read")
	}
}

func TestSamplerPreservesProviderTimeoutAndFinalFenceAfterSlowResolution(t *testing.T) {
	lister := &samplerLister{pages: map[string]SessionPage{
		"": {Sessions: []SessionIdentity{{TenantID: "tenant", SessionID: "session"}}},
	}}
	resolver := delayedSamplerResolver{
		samplerLister: lister,
		delay:         historyOwnershipCheckTimeout + 25*time.Millisecond,
		target: Target{
			EnvironmentID: "environment", Mode: ModeManaged,
			Instance: Instance{AllocationID: "allocation", ProviderKey: "provider", AllocationState: "running"},
		},
	}
	records := make(chan ExportRecord, 1)
	service, err := NewService(
		resolver,
		map[string]SourceResolver{"provider": blockingSource{}},
		WithExporter(channelExporter{records: records}, ExportOptions{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	owner := &sequenceOwner{}
	sampler, err := NewSampler(resolver, service, owner, SamplerOptions{
		Interval: time.Minute, SourceTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := sampler.sweep(t.Context())
	if !result.Complete || result.Observed != 1 || result.Failed != 0 {
		t.Fatalf("slow-resolution sweep lost the timeout observation: %+v", result)
	}
	select {
	case record := <-records:
		if record.Status != StatusUnavailable || record.Reason != "sample_timeout" || record.CollectionSource != CollectionSourcePeriodic {
			t.Fatalf("slow-resolution timeout export mismatch: %+v", record)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for slow-resolution timeout export")
	}
	if err := service.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
