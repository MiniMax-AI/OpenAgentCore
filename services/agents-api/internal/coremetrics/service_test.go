package coremetrics

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixtureSource struct {
	history History
	err     error
	sample  Sample
	live    Live
}

func (f *fixtureSource) Sample(context.Context) Sample { return f.sample }
func (f *fixtureSource) History(context.Context, time.Time, time.Time, time.Duration) (History, error) {
	return f.history, f.err
}
func (f *fixtureSource) Live() Live { return f.live }
func fixtureService(t *testing.T) (*Service, *fixtureSource, time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 25, 12, 0, 20, 0, time.UTC)
	source := &fixtureSource{history: History{Buckets: map[time.Time]*float64{}}, live: Live{ExecutionOwner: ptr(true), SlotsInUse: ptr(int64(2)), SlotsTotal: ptr(int64(4))}}
	service := New(now.Add(-2*time.Hour), strings.Repeat("a", 40), source)
	service.now = func() time.Time { return now }
	return service, source, now
}
func TestCompleteBucketsAndObservedPercentiles(t *testing.T) {
	s, source, now := fixtureService(t)
	end := now.Truncate(time.Minute)
	// The first two probes finish in the same 30s slot after differing I/O delays.
	for _, sample := range []Sample{
		{At: end.Add(-27 * time.Second), Queued: ptr(int64(3)), InProgress: ptr(int64(1)), PingMS: ptr(2.0), PoolInUse: ptr(int64(2)), Healthy: true},
		{At: end.Add(-time.Second), Queued: ptr(int64(1)), InProgress: ptr(int64(4)), PingMS: ptr(6.0), PoolInUse: ptr(int64(3)), Healthy: true},
		{At: now, Queued: ptr(int64(99)), Healthy: true},
	} {
		s.record(sample)
	}
	source.history = History{Interrupted: 2, QueueWaitMS: Latency{P50: ptr(100.0), P95: ptr(200.0)}, Buckets: map[time.Time]*float64{end.Add(-time.Minute): ptr(200.0)}}
	s.now = func() time.Time { return end.Add(-10 * time.Second) }
	s.RecordUnavailable()
	s.now = func() time.Time { return now }
	s.RecordUnavailable()
	got, err := s.Read(t.Context(), "1h")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Range.End.Equal(end) || len(got.Execution.Series) != 60 || *got.Execution.QueuedTurns != 99 || *got.Execution.Unavailable != 1 {
		t.Fatal(got)
	}
	b := got.Execution.Series[59]
	d := got.Database.Series[59]
	if *b.Queued != 3 || *b.InProgress != 4 || *b.QueueWaitP95MS != 200 || *d.PoolInUse != 3 || *d.PingP95MS != 5.8 || *got.Database.PingMS.P50 != 4 {
		t.Fatal(b, d, got.Database.PingMS)
	}
	if got.Execution.Series[0].Queued != nil || got.Database.Series[0].PingP95MS != nil {
		t.Fatal("missing buckets became zero")
	}
}
func TestUnknownAndFailureRemainNull(t *testing.T) {
	s, source, now := fixtureService(t)
	s.started = now.Add(-20 * time.Second)
	source.err = errors.New("private database information")
	s.record(Sample{At: now.Add(-10 * time.Second), PingMS: ptr(1.0), Queued: ptr(int64(0)), Healthy: false})
	got, err := s.Read(t.Context(), "1h")
	if err != nil {
		t.Fatal(err)
	}
	if got.Service.Status != "degraded" || got.Execution.Unavailable != nil || got.Execution.Interrupted != nil || got.Execution.QueueWaitMS.P50 != nil || got.Database.SizeBytes != nil {
		t.Fatal(got)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "private") || !strings.Contains(string(raw), `"unavailable":null`) {
		t.Fatal(string(raw))
	}
	for _, bucket := range got.Execution.Series {
		if bucket.Queued != nil {
			t.Fatal("pre-start bucket was filled", bucket)
		}
	}
	s.started = now.Add(-time.Hour)
	s.latest.At = now.Add(-3 * SampleInterval)
	got, _ = s.Read(t.Context(), "1h")
	if got.Execution.QueuedTurns != nil {
		t.Fatal("stale gauge remained current")
	}
}
func TestAllRangesAndBoundedRetention(t *testing.T) {
	s, _, now := fixtureService(t)
	for name, count := range map[string]int{"1h": 60, "6h": 72, "24h": 96, "7d": 84} {
		got, err := s.Read(t.Context(), name)
		if err != nil || len(got.Execution.Series) != count || len(got.Database.Series) != count {
			t.Fatal(name, err)
		}
	}
	if _, err := s.Read(t.Context(), "30d"); err == nil {
		t.Fatal("unbounded range accepted")
	}
	// More than a retention window overwrites fixed slots, independent of load.
	old := now.Add(-8 * 24 * time.Hour)
	s.now = func() time.Time { return old }
	s.RecordUnavailable()
	s.record(Sample{At: old, Queued: ptr(int64(20))})
	s.now = func() time.Time { return now }
	s.started = old
	got, _ := s.Read(t.Context(), "7d")
	if *got.Execution.Unavailable != 0 {
		t.Fatal("expired refusal counted")
	}
	for _, v := range got.Execution.Series {
		if v.Queued != nil {
			t.Fatal("expired gauge counted")
		}
	}
}
func TestJobResultsAndConcurrentReads(t *testing.T) {
	s, _, now := fixtureService(t)
	s.record(Sample{At: now, Healthy: true, Maintenance: ptr(true)})
	s.ReportJob("audit_cleanup", now, ptr(int64(12)), ptr(int64(0)), nil)
	got, _ := s.Read(t.Context(), "1h")
	if got.Service.Status != "maintenance" || *got.Jobs[3].Processed != 12 {
		t.Fatal(got.Service, got.Jobs)
	}
	s.ReportJob("audit_cleanup", now, ptr(int64(2)), ptr(int64(1)), errors.New("failure"))
	got, _ = s.Read(t.Context(), "1h")
	if got.Service.Status != "degraded" {
		t.Fatal(got.Service)
	}
	s.StopJob("audit_cleanup")
	got, _ = s.Read(t.Context(), "1h")
	if got.Jobs[3].Status != "stopped" {
		t.Fatal(got.Jobs)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				s.RecordUnavailable()
				s.ReportJob("history_cleanup", now, ptr(int64(0)), ptr(int64(0)), nil)
				if _, err := s.Read(context.Background(), "1h"); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
}
func TestRevisionMustBeCommit(t *testing.T) {
	for _, revision := range []string{"", "unknown", "secret-value"} {
		s := New(time.Now(), revision, &fixtureSource{})
		if s.revision != nil {
			t.Fatal("unverified build revision exposed")
		}
	}
}

func TestSevenDayAlignedWindowRetainsLeadingSamples(t *testing.T) {
	s, _, now := fixtureService(t)
	now = now.Add(time.Hour + 59*time.Minute)
	window, err := Window(now, "7d")
	if err != nil {
		t.Fatal(err)
	}
	s.started = window.Start.Add(-time.Hour)
	first := window.Start.Add(10 * time.Second)
	s.now = func() time.Time { return first }
	s.RecordUnavailable()
	s.record(Sample{At: first, Queued: ptr(int64(7)), Healthy: true})
	// Populate every slot through the present, crossing the ordinary 7d cutoff.
	for at := first.Add(SampleInterval); !at.After(now); at = at.Add(SampleInterval) {
		s.record(Sample{At: at, Queued: ptr(int64(0)), Healthy: true})
		s.now = func() time.Time { return at }
		s.RecordUnavailable()
	}
	s.now = func() time.Time { return now }
	got, err := s.Read(t.Context(), "7d")
	if err != nil || got.Execution.Unavailable == nil || *got.Execution.Unavailable != int64(retention/SampleInterval) || got.Execution.Series[0].Queued == nil || *got.Execution.Series[0].Queued != 7 {
		t.Fatal("leading complete bucket was overwritten", err, got.Execution.Unavailable, got.Execution.Series[0])
	}
}
