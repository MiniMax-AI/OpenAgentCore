package observability

import (
	"context"
	"testing"
	"time"
)

type capturedRequests struct {
	buckets []RequestBucket
	dropped int64
}

func (s *capturedRequests) WriteRequestBuckets(_ context.Context, buckets []RequestBucket, dropped, _ int64) error {
	s.buckets = append(s.buckets, buckets...)
	s.dropped += dropped
	return nil
}

func TestRequestRecorderAggregatesBoundedLatencyBuckets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sink := &capturedRequests{}
	recorder := NewRequestRecorder(ctx, sink)
	at := time.Date(2026, 9, 25, 10, 20, 30, 0, time.UTC)
	recorder.Record(Request{CompletedAt: at, RouteFamily: "sessions", Method: "GET", Outcome: "success", Latency: 50 * time.Millisecond})
	recorder.Record(Request{CompletedAt: at.Add(3 * time.Second), RouteFamily: "sessions", Method: "GET", Outcome: "success", Latency: 11 * time.Second})
	cancel()
	select {
	case <-recorder.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("request recorder did not drain on shutdown")
	}
	if len(sink.buckets) != 1 {
		t.Fatalf("expected one aggregate bucket, got %d", len(sink.buckets))
	}
	bucket := sink.buckets[0]
	if bucket.Count != 2 || bucket.Start != at.Truncate(time.Minute) || bucket.LatencySumMS != 11050 || bucket.LatencyCounts[2] != 1 || bucket.LatencyCounts[10] != 1 || sink.dropped != 0 {
		t.Fatalf("unexpected request aggregate: %+v, dropped=%d", bucket, sink.dropped)
	}
}
