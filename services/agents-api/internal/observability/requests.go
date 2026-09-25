// Package observability collects bounded, side-channel operator metrics.
package observability

import (
	"context"
	"sync/atomic"
	"time"
)

// Request contains only bounded labels; it never carries a URL, principal, or payload.
type Request struct {
	CompletedAt time.Time
	RouteFamily string
	Method      string
	Outcome     string
	Latency     time.Duration
}

type RequestBucket struct {
	Start         time.Time
	RouteFamily   string
	Method        string
	Outcome       string
	Count         int64
	LatencySumMS  int64
	LatencyCounts [11]int64
}

type RequestSink interface {
	WriteRequestBuckets(context.Context, []RequestBucket, int64, int64) error
}

// RequestRecorder batches metrics away from the HTTP response path.
type RequestRecorder struct {
	queue   chan Request
	sink    RequestSink
	dropped atomic.Int64
	failed  atomic.Int64
	done    chan struct{}
}

func NewRequestRecorder(ctx context.Context, sink RequestSink) *RequestRecorder {
	r := &RequestRecorder{queue: make(chan Request, 2048), sink: sink, done: make(chan struct{})}
	go r.run(ctx)
	return r
}

func (r *RequestRecorder) Record(value Request) {
	select {
	case r.queue <- value:
	default:
		r.dropped.Add(1)
	}
}

func (r *RequestRecorder) Done() <-chan struct{} { return r.done }

func (r *RequestRecorder) run(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	pending := make(map[RequestBucket]*RequestBucket)
	var lastHeartbeat time.Time
	flush := func() {
		buckets := make([]RequestBucket, 0, len(pending))
		var pendingCount int64
		for _, value := range pending {
			buckets = append(buckets, *value)
			pendingCount += value.Count
		}
		dropped := r.dropped.Swap(0)
		failed := r.failed.Swap(0)
		minute := time.Now().UTC().Truncate(time.Minute)
		if len(buckets) == 0 && dropped == 0 && failed == 0 && minute.Equal(lastHeartbeat) {
			return
		}
		writeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := r.sink.WriteRequestBuckets(writeCtx, buckets, dropped, failed)
		cancel()
		if err != nil {
			r.failed.Add(1)
			r.dropped.Add(dropped + pendingCount)
		} else {
			lastHeartbeat = minute
		}
		clear(pending)
	}
	add := func(value Request) {
		if value.CompletedAt.IsZero() {
			return
		}
		start := value.CompletedAt.UTC().Truncate(time.Minute)
		key := RequestBucket{Start: start, RouteFamily: value.RouteFamily, Method: value.Method, Outcome: value.Outcome}
		bucket := pending[key]
		if bucket == nil {
			bucket = &key
			pending[key] = bucket
		}
		bucket.Count++
		ms := value.Latency.Milliseconds()
		if ms < 0 {
			ms = 0
		}
		bucket.LatencySumMS += ms
		bucket.LatencyCounts[latencyIndex(ms)]++
	}
	for {
		select {
		case value := <-r.queue:
			add(value)
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			for {
				select {
				case value := <-r.queue:
					add(value)
				default:
					flush()
					return
				}
			}
		}
	}
}

func latencyIndex(ms int64) int {
	for i, bound := range [...]int64{10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000} {
		if ms <= bound {
			return i
		}
	}
	return 10
}
