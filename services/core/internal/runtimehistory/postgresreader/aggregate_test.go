package postgresreader

import (
	"math"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/google/uuid"
)

func TestAggregateSeriesUsesProviderObservationOrder(t *testing.T) {
	start := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	query := testQuery(start)
	startedAt := start.Add(-time.Minute)
	firstObserved, secondObserved := start.Add(5*time.Second), start.Add(15*time.Second)
	firstResolved, secondResolved := start.Add(20*time.Second), start.Add(10*time.Second)
	samples := []*rawSample{
		{
			resolvedAt: secondResolved, observedAt: &secondObserved, allocation: testAllocation, startedAt: &startedAt,
			provider: "docker", status: runtimeobs.StatusObserved, hasSample: true,
			metrics: map[string]float64{CPUUsageName: 3, CPUCapacityName: 2, MemoryUsageName: 200},
		},
		{
			resolvedAt: firstResolved, observedAt: &firstObserved, allocation: testAllocation, startedAt: &startedAt,
			provider: "docker", status: runtimeobs.StatusObserved, hasSample: true,
			metrics: map[string]float64{CPUUsageName: 1, CPUCapacityName: 2, MemoryUsageName: 100},
		},
	}
	points, err := aggregateSeries(query, samples)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].CPUUtilizationRatio == nil || *points[0].CPUUtilizationRatio != .1 || points[0].MemoryUsageBytes == nil || *points[0].MemoryUsageBytes != 200 {
		t.Fatalf("provider observation order was not preserved: %+v", points)
	}
}

func TestAggregateSeriesLeavesCPUUsageGapWhenEitherEndpointLacksCapacity(t *testing.T) {
	start := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	query := testQuery(start)
	startedAt := start.Add(-time.Minute)
	firstObserved, secondObserved, thirdObserved := start.Add(-5*time.Second), start.Add(5*time.Second), start.Add(15*time.Second)
	samples := []*rawSample{
		{
			resolvedAt: firstObserved, observedAt: &firstObserved, allocation: testAllocation, startedAt: &startedAt,
			provider: "docker", status: runtimeobs.StatusObserved, hasSample: true,
			metrics: map[string]float64{CPUUsageName: 1, CPUCapacityName: 2},
		},
		{
			resolvedAt: secondObserved, observedAt: &secondObserved, allocation: testAllocation, startedAt: &startedAt,
			provider: "docker", status: runtimeobs.StatusObserved, hasSample: true,
			metrics: map[string]float64{CPUUsageName: 3},
		},
		{
			resolvedAt: thirdObserved, observedAt: &thirdObserved, allocation: testAllocation, startedAt: &startedAt,
			provider: "docker", status: runtimeobs.StatusObserved, hasSample: true,
			metrics: map[string]float64{CPUUsageName: 5, CPUCapacityName: 2},
		},
	}
	points, err := aggregateSeries(query, samples)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].CPUUtilizationRatio != nil {
		t.Fatalf("missing CPU capacity was synthesized into utilization: %+v", points)
	}
}

func TestAggregateIgnoresLookbackOnlySeriesForSeriesLimit(t *testing.T) {
	start := time.Date(2026, 9, 23, 8, 0, 0, 0, time.UTC)
	query := testQuery(start)
	query.MaximumSeries = 64
	raw := make([]*rawSample, 0, 66)
	for range 65 {
		startedAt := start.Add(-time.Minute)
		observedAt := start.Add(-2 * time.Second)
		raw = append(raw, &rawSample{
			resolvedAt: start.Add(-time.Second), observedAt: &observedAt, allocation: uuid.NewString(), startedAt: &startedAt,
			provider: "docker", status: runtimeobs.StatusObserved, hasSample: true, metrics: map[string]float64{},
		})
	}
	startedAt := start.Add(-time.Minute)
	observedAt := start.Add(5 * time.Second)
	raw = append(raw, &rawSample{
		resolvedAt: start.Add(6 * time.Second), observedAt: &observedAt, allocation: uuid.NewString(), startedAt: &startedAt,
		provider: "docker", status: runtimeobs.StatusObserved, hasSample: true, metrics: map[string]float64{},
	})
	result, err := aggregate(query, start.Add(time.Minute), raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Series) != 1 || len(result.Series[0].Points) != 1 {
		t.Fatalf("lookback-only incarnations consumed returned-series budget: %+v", result.Series)
	}
}

func TestAggregateSeriesAveragesProviderReportedUtilization(t *testing.T) {
	start := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	query := testQuery(start)
	startedAt := start.Add(-time.Minute)
	samples := []*rawSample{}
	for index, ratio := range []float64{.2, .4} {
		observedAt := start.Add(time.Duration(5+index*15) * time.Second)
		samples = append(samples, &rawSample{
			resolvedAt: observedAt, observedAt: &observedAt, allocation: testAllocation, startedAt: &startedAt,
			provider: "e2b", status: runtimeobs.StatusObserved, hasSample: true,
			metrics: map[string]float64{CPUUtilizationName: ratio, CPUCapacityName: 2, MemoryUsageName: 100},
		})
	}
	points, err := aggregateSeries(query, samples)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].CPUUtilizationRatio == nil || math.Abs(*points[0].CPUUtilizationRatio-.3) > 1e-12 ||
		points[0].CPUContributorCount != 2 || *points[0].CPUCapacityCores != 2 {
		t.Fatalf("reported utilization was not kept per bucket: %+v", points)
	}
}
