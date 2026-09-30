package coremetrics

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestProcessCPUIntervals(t *testing.T) {
	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	var sampler processSampler
	for _, test := range []struct {
		seconds int
		cpu     *float64
		want    *float64
	}{
		{0, ptr(10.0), nil},
		{30, ptr(25.0), ptr(.5)},
		{60, ptr(25.0), ptr(0.0)},
		{90, ptr(1.0), nil}, // Counter reset.
		{120, ptr(31.0), ptr(1.0)},
		{120, ptr(32.0), nil}, // No elapsed time.
		{110, ptr(33.0), nil}, // Clock moved backwards.
		{180, ptr(35.0), nil}, // Missing interval.
		{210, nil, nil},
		{240, ptr(40.0), nil},
		{270, ptr(math.NaN()), nil},
		{300, ptr(50.0), nil},
		{330, ptr(math.Inf(1)), nil},
		{360, ptr(-1.0), nil},
		{390, ptr(60.0), nil},
		{420, ptr(120.0), ptr(2.0)},
	} {
		got := sampler.sample(start.Add(time.Duration(test.seconds)*time.Second), processReading{cpuSeconds: test.cpu, rssBytes: ptr(uint64(1024)), memoryLimitBytes: ptr(uint64(4096)), cpuLimitCores: ptr(2.0)})
		if (got.CPUCores == nil) != (test.want == nil) || (test.want != nil && *got.CPUCores != *test.want) {
			t.Fatalf("at %ds: cpu=%v, want=%v", test.seconds, got.CPUCores, test.want)
		}
		if *got.RSSBytes != 1024 || *got.MemoryLimitBytes != 4096 || *got.CPULimitCores != 2 {
			t.Fatal("independent gauges lost", got)
		}
	}
}

func TestProcessSeriesAndFreshness(t *testing.T) {
	s, _, now := fixtureService(t)
	end := now.Truncate(time.Minute)
	s.started = end.Add(-8 * 24 * time.Hour)
	s.record(Sample{At: end.Add(-30 * time.Second), Healthy: true, Process: Process{CPUCores: ptr(.8), RSSBytes: ptr(uint64(100))}})
	s.record(Sample{At: end.Add(-time.Second), Healthy: true, Process: Process{CPUCores: ptr(.3), RSSBytes: ptr(uint64(200))}})
	s.record(Sample{At: now, Healthy: true, Process: Process{CPUCores: ptr(9.0), RSSBytes: ptr(uint64(999)), CPULimitCores: ptr(2.0), MemoryLimitBytes: ptr(uint64(4096))}})
	for name, count := range map[string]int{"1h": 60, "6h": 72, "24h": 96, "7d": 84} {
		view, err := s.Read(t.Context(), name)
		if err != nil || len(view.Process.Series) != count {
			t.Fatal(name, err, len(view.Process.Series))
		}
		if *view.Process.CPUCores != 9 || *view.Process.RSSBytes != 999 || *view.Process.CPULimitCores != 2 || *view.Process.MemoryLimitBytes != 4096 || view.Process.MemoryBytes == nil || view.Process.Goroutines == nil {
			t.Fatal("current or existing gauges lost", view.Process)
		}
		last := view.Process.Series[count-1]
		if last.CPUCores == nil || last.RSSBytes == nil || *last.CPUCores != .8 || *last.RSSBytes != 200 || !last.Start.Equal(view.Range.End.Add(-time.Duration(view.Range.ResolutionSeconds)*time.Second)) {
			t.Fatal("complete bucket maxima", last)
		}
		if view.Process.Series[0].CPUCores != nil || view.Process.Series[0].RSSBytes != nil {
			t.Fatal("missing history became zero")
		}
	}
	s.started = end.Add(-30 * time.Second)
	view, _ := s.Read(t.Context(), "1h")
	if view.Process.Series[59].CPUCores != nil || view.Process.Series[59].RSSBytes != nil {
		t.Fatal("partial first bucket was filled")
	}
	s.now = func() time.Time { return now.Add(2*SampleInterval + time.Second) }
	view, _ = s.Read(t.Context(), "1h")
	if view.Process.CPUCores != nil || view.Process.RSSBytes != nil || view.Process.CPULimitCores != nil || view.Process.MemoryLimitBytes != nil {
		t.Fatal("stale process values remained current")
	}
	raw, err := json.Marshal(view.Process)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"cpu_cores", "rss_bytes", "cpu_limit_cores", "memory_limit_bytes"} {
		if !strings.Contains(string(raw), `"`+key+`":null`) {
			t.Fatal("unknown fields must be present as null", string(raw))
		}
	}
}
