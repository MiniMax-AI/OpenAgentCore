package store

import (
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/observability"
)

func TestPostgresOperatorRequestBuckets(t *testing.T) {
	s, _ := testStore(t)
	start := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	first := observability.RequestBucket{Start: start, RouteFamily: "sessions", Method: "GET", Outcome: "success", Count: 2, LatencySumMS: 70}
	first.LatencyCounts[1], first.LatencyCounts[2] = 1, 1
	second := observability.RequestBucket{Start: start, RouteFamily: "sessions", Method: "GET", Outcome: "success", Count: 1, LatencySumMS: 300}
	second.LatencyCounts[5] = 1
	if err := s.WriteRequestBuckets(t.Context(), []observability.RequestBucket{first}, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteRequestBuckets(t.Context(), []observability.RequestBucket{second}, 1, 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadOperatorMetrics(t.Context(), start, start.Add(time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Requests) != 1 || got.Requests[0].Count != 3 || got.Requests[0].LatencySumMS != 370 ||
		got.Requests[0].LatencyCounts[1] != 1 || got.Requests[0].LatencyCounts[2] != 1 || got.Requests[0].LatencyCounts[5] != 1 {
		t.Fatalf("request bucket lost concurrent-shape additions: %+v", got.Requests)
	}
	// Collector health is written in the current minute, separate from the
	// completed request's event-time bucket.
	health, err := s.ReadOperatorMetrics(t.Context(), start, start.Add(2*time.Minute), time.Minute)
	if err != nil || len(health.Collector) == 0 {
		t.Fatalf("collector health unavailable: %+v, %v", health.Collector, err)
	}
}
