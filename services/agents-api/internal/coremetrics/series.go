package coremetrics

import (
	"math"
	"sort"
	"time"
)

// Caller holds mu. Gauges are the highest observed value, not an interpolated
// history. Missing buckets, including the process's partial first bucket, stay null.
func (s *Service) series(view *View) {
	step := time.Duration(view.Range.ResolutionSeconds) * time.Second
	count := int(view.Range.End.Sub(view.Range.Start) / step)
	view.Execution.Series = make([]ExecutionBucket, count)
	view.Database.Series = make([]DatabaseBucket, count)
	pings := make([][]float64, count)
	var allPings []float64
	for i := 0; i < count; i++ {
		start := view.Range.Start.Add(time.Duration(i) * step)
		view.Execution.Series[i].Start = start
		view.Database.Series[i].Start = start
	}
	for _, sample := range s.samples {
		if sample.At.Before(view.Range.Start) || !sample.At.Before(view.Range.End) || sample.At.IsZero() {
			continue
		}
		i := int(sample.At.Sub(view.Range.Start) / step)
		if sample.PingMS != nil {
			allPings = append(allPings, *sample.PingMS)
		}
		if view.Execution.Series[i].Start.Before(s.started) {
			continue
		}
		maxInto(&view.Execution.Series[i].Queued, sample.Queued)
		maxInto(&view.Execution.Series[i].InProgress, sample.InProgress)
		maxInto(&view.Database.Series[i].PoolInUse, sample.PoolInUse)
		if sample.PingMS != nil {
			pings[i] = append(pings[i], *sample.PingMS)
		}
	}
	for i := range pings {
		view.Database.Series[i].PingP95MS = percentile(pings[i], .95)
	}
	view.Database.PingMS = Latency{P50: percentile(allPings, .5), P95: percentile(allPings, .95)}
	// A lifetime counter cannot prove the missing part of a pre-start range.
	if !view.Range.Start.Before(s.started) {
		total := int64(0)
		for _, slot := range s.refusals {
			at := time.Unix(slot.tick*int64(SampleInterval/time.Second), 0)
			if !at.Before(view.Range.Start) && at.Before(view.Range.End) {
				total += slot.count
			}
		}
		view.Execution.Unavailable = &total
	}
}
func maxInto(target **int64, value *int64) {
	if value != nil && (*target == nil || **target < *value) {
		*target = ptr(*value)
	}
}
func percentile(values []float64, quantile float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sort.Float64s(values)
	n := float64(len(values)-1) * quantile
	lo, hi := int(math.Floor(n)), int(math.Ceil(n))
	return ptr(values[lo] + (values[hi]-values[lo])*(n-float64(lo)))
}
