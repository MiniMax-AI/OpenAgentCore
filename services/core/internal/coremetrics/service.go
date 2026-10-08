package coremetrics

import (
	"context"
	"errors"
	"regexp"
	"runtime"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

const SampleInterval = 30 * time.Second
const retention = 7 * 24 * time.Hour

// The 7d window ends at a complete 2h bucket, so retain its leading padding too.
const sampleCapacity = int((retention+2*time.Hour)/SampleInterval) + 2

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
	jobIDs     []string
	jobs       map[string]Job
	periodic   []Periodic
	process    processSampler
}

// Periodic is a background job the metrics report. Run makes one bounded pass
// and returns what it processed, nil when the pass counted nothing, and what
// failed. The next pass starts Every after a pass ends; a panic fails that pass
// only. A job without Run is disabled and reports stopped.
type Periodic struct {
	ID    string
	Every time.Duration
	Run   func(context.Context) (processed *int64, failed int64, err error)
}

// errPanicked is the error of a pass that panicked.
var errPanicked = errors.New("periodic job pass panicked")

// New reports the scheduler, which the Source reads live, and then jobs in
// their order. Run runs the jobs. An enabled job needs a positive Every.
func New(started time.Time, revision string, source Source, jobs ...Periodic) (*Service, error) {
	s := &Service{source: source, started: started.UTC(), now: time.Now, jobIDs: []string{"scheduler"}, jobs: map[string]Job{"scheduler": {ID: "scheduler", Status: JobUnknown}}, periodic: jobs}
	if revisionPattern.MatchString(revision) {
		s.revision = &revision
	}
	for _, job := range jobs {
		status := JobUnknown
		if job.Run == nil {
			status = JobStopped
		} else if job.Every <= 0 {
			return nil, errors.New("coremetrics: periodic job " + job.ID + " needs a positive interval")
		}
		s.jobIDs = append(s.jobIDs, job.ID)
		s.jobs[job.ID] = Job{ID: job.ID, Status: status}
	}
	return s, nil
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
func (s *Service) reportJob(id string, at time.Time, processed *int64, failed *int64, err error) {
	status := JobOk
	if err != nil || (failed != nil && *failed > 0) {
		status = JobFailing
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return
	}
	s.jobs[id] = Job{ID: id, Status: status, LastRunAt: ptr(at.UTC()), Processed: processed, Failed: failed}
}
func (s *Service) stopJob(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		j.Status = JobStopped
		s.jobs[id] = j
	}
}

// Run samples Core and runs every enabled job until ctx ends, then waits for
// them to stop.
func (s *Service) Run(ctx context.Context) {
	var jobs sync.WaitGroup
	for _, job := range s.periodic {
		if job.Run != nil {
			jobs.Go(func() { s.runJob(ctx, job) })
		}
	}
	s.sample(ctx)
	jobs.Wait()
}

func (s *Service) runJob(ctx context.Context, job Periodic) {
	defer s.stopJob(job.ID)
	for {
		s.pass(ctx, job)
		select {
		case <-ctx.Done():
			return
		case <-time.After(job.Every):
		}
	}
}

// pass runs and reports one pass of job. A panic is reported as a failed pass.
func (s *Service) pass(ctx context.Context, job Periodic) {
	var processed *int64
	failed, err := int64(1), errPanicked
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Ctx(ctx).Error("Periodic job panicked", "job", job.ID, "panic", recovered)
		}
		s.reportJob(job.ID, s.now(), processed, &failed, err)
	}()
	processed, failed, err = job.Run(ctx)
}

func (s *Service) sample(ctx context.Context) {
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
		return Range{}, ErrInvalidRange
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
	view := View{Object: "core.metrics", Range: window, Service: ServiceState{Status: ServiceRunning, Revision: s.revision, StartedAt: ptr(s.started), ExecutionOwner: live.ExecutionOwner},
		Execution: Execution{SlotsInUse: live.SlotsInUse, SlotsTotal: live.SlotsTotal, ConnectedDaemons: live.ConnectedDaemons}, Database: Database{Pool: live.Pool}, Jobs: make([]Job, 0, len(s.jobIDs))}
	s.mu.Lock()
	latest := s.latest
	if latest.At.IsZero() || now.Sub(latest.At) > 2*SampleInterval || !latest.Healthy {
		view.Service.Status = ServiceDegraded
	}
	if !latest.At.IsZero() && now.Sub(latest.At) <= 2*SampleInterval {
		view.Execution.QueuedTurns, view.Execution.WaitingForDaemon, view.Execution.InProgressTurns = latest.Queued, latest.WaitingForDaemon, latest.InProgress
		view.Execution.OldestQueuedSeconds = latest.OldestQueuedSeconds
		view.Database.SizeBytes = latest.DatabaseSize
		view.Process = latest.Process

	}
	for _, id := range s.jobIDs {
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
		view.Service.Status = ServiceDegraded
	} else {
		view.Execution.Interrupted = ptr(history.Interrupted)
		view.Execution.QueueWaitMS = history.QueueWaitMS
		for i := range view.Execution.Series {
			view.Execution.Series[i].QueueWaitP95MS = history.Buckets[view.Execution.Series[i].Start]
		}
	}
	if live.ExecutionOwner == nil || !*live.ExecutionOwner {
		view.Service.Status = ServiceDegraded
	}
	for _, job := range view.Jobs {
		if job.Status == JobFailing {
			view.Service.Status = ServiceDegraded
		}
	}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	view.Process.MemoryBytes = ptr(memory.Alloc)
	view.Process.Goroutines = ptr(int64(runtime.NumGoroutine()))
	return view, nil
}
func ptr[T any](v T) *T { return &v }
