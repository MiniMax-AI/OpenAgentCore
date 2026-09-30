package coremetrics

import (
	"context"
	"errors"
	"regexp"
	"runtime"
	"sync"
	"time"
)

const SampleInterval = 30 * time.Second
const retention = 7 * 24 * time.Hour

// The 7d window ends at a complete 2h bucket, so retain its leading padding too.
const sampleCapacity = int((retention+2*time.Hour)/SampleInterval) + 2

var jobIDs = []string{"scheduler", "runtime_sampler", "history_cleanup", "audit_cleanup"}
var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type refusalSlot struct {
	tick  int64
	count int64
}
type Service struct {
	mu         sync.Mutex
	source     Source
	started    time.Time
	revision   *string
	now        func() time.Time
	samples    [sampleCapacity]Sample
	nextSample int
	refusals   [sampleCapacity]refusalSlot
	latest     Sample
	jobs       map[string]Job
	process    processSampler
}

func New(started time.Time, revision string, source Source) *Service {
	s := &Service{source: source, started: started.UTC(), now: time.Now, jobs: map[string]Job{}}
	if revisionPattern.MatchString(revision) {
		s.revision = &revision
	}
	for _, id := range jobIDs {
		s.jobs[id] = Job{ID: id, Status: "unknown"}
	}
	return s
}
func slot(t time.Time) (int64, int) {
	tick := t.Unix() / int64(SampleInterval/time.Second)
	return tick, int(tick % int64(sampleCapacity))
}

// RecordUnavailable is called only when Core emits this exact rejection code.
// Fixed slots bound memory independently of request volume.
func (s *Service) RecordUnavailable() {
	tick, i := slot(s.now())
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refusals[i].tick != tick {
		s.refusals[i] = refusalSlot{tick: tick}
	}
	s.refusals[i].count++
}
func (s *Service) ReportJob(id string, at time.Time, processed *int64, failed *int64, err error) {
	status := "ok"
	if err != nil || (failed != nil && *failed > 0) {
		status = "failing"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return
	}
	s.jobs[id] = Job{ID: id, Status: status, LastRunAt: ptr(at.UTC()), Processed: processed, Failed: failed}
}
func (s *Service) StopJob(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.Status = "stopped"
		s.jobs[id] = j
	}
}
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(SampleInterval)
	defer ticker.Stop()
	for {
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		sample := s.source.Sample(probe)
		cancel()
		at := s.now()
		sample.Process = s.process.sample(at, readProcess())
		sample.At = at.UTC()
		s.record(sample)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Service) record(sample Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Consecutive probes can finish within the same wall-clock slot. Keep both.
	s.samples[s.nextSample] = sample
	s.nextSample = (s.nextSample + 1) % len(s.samples)
	s.latest = sample
}
func Window(now time.Time, name string) (Range, error) {
	var duration, step time.Duration
	switch name {
	case "1h":
		duration, step = time.Hour, time.Minute
	case "6h":
		duration, step = 6*time.Hour, 5*time.Minute
	case "24h":
		duration, step = 24*time.Hour, 15*time.Minute
	case "7d":
		duration, step = retention, 2*time.Hour
	default:
		return Range{}, errors.New("invalid Core metrics range")
	}
	end := now.UTC().Truncate(step)
	return Range{Start: end.Add(-duration), End: end, ResolutionSeconds: int64(step / time.Second)}, nil
}
func (s *Service) Read(ctx context.Context, name string) (View, error) {
	now := s.now().UTC()
	window, err := Window(now, name)
	if err != nil {
		return View{}, err
	}
	live := s.source.Live()
	view := View{Object: "core.metrics", Range: window, Service: ServiceState{Status: "running", Revision: s.revision, StartedAt: ptr(s.started), ExecutionOwner: live.ExecutionOwner},
		Execution: Execution{SlotsInUse: live.SlotsInUse, SlotsTotal: live.SlotsTotal, ConnectedDaemons: live.ConnectedDaemons}, Database: Database{Pool: live.Pool}, Jobs: make([]Job, 0, len(jobIDs))}
	s.mu.Lock()
	latest := s.latest
	if latest.At.IsZero() || now.Sub(latest.At) > 2*SampleInterval || !latest.Healthy {
		view.Service.Status = "degraded"
	}
	if !latest.At.IsZero() && now.Sub(latest.At) <= 2*SampleInterval {
		view.Execution.QueuedTurns, view.Execution.WaitingForDaemon, view.Execution.InProgressTurns = latest.Queued, latest.WaitingForDaemon, latest.InProgress
		view.Execution.OldestQueuedSeconds = latest.OldestQueuedSeconds
		view.Database.SizeBytes = latest.DatabaseSize
		view.Process = latest.Process

	}
	for _, id := range jobIDs {
		j := s.jobs[id]
		if id == "scheduler" && live.Scheduler.ID != "" {
			j = live.Scheduler
		}
		view.Jobs = append(view.Jobs, j)
	}
	s.series(&view)
	s.mu.Unlock()
	query, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	history, err := s.source.History(query, window.Start, window.End, time.Duration(window.ResolutionSeconds)*time.Second)
	if err != nil {
		view.Service.Status = "degraded"
	} else {
		view.Execution.Interrupted = ptr(history.Interrupted)
		view.Execution.QueueWaitMS = history.QueueWaitMS
		for i := range view.Execution.Series {
			view.Execution.Series[i].QueueWaitP95MS = history.Buckets[view.Execution.Series[i].Start]
		}
	}
	if live.ExecutionOwner == nil || (live.SlotsTotal != nil && *live.SlotsTotal > 0 && !*live.ExecutionOwner) {
		view.Service.Status = "degraded"
	}
	for _, job := range view.Jobs {
		if job.Status == "failing" {
			view.Service.Status = "degraded"
		}
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	view.Process.MemoryBytes = ptr(memory.Alloc)
	view.Process.Goroutines = ptr(int64(runtime.NumGoroutine()))
	return view, nil
}
func ptr[T any](v T) *T { return &v }
