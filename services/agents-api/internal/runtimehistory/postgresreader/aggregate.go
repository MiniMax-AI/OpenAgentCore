// Package postgresreader stores and reads bounded periodic Runtime telemetry.
package postgresreader

import (
	"errors"
	"math"
	"sort"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/google/uuid"
)

const (
	CPUUsageName    = "cpu_usage_seconds"
	CPUCapacityName = "cpu_capacity_cores"
	MemoryUsageName = "memory_usage_bytes"
	MemoryLimitName = "memory_limit_bytes"
)

type rawSample struct {
	resolvedAt time.Time
	observedAt *time.Time
	allocation string
	startedAt  *time.Time
	provider   string
	status     runtimeobs.Status
	hasSample  bool
	metrics    map[string]float64
}

type rawTokenUsage struct {
	resolvedAt                time.Time
	inputTokens, outputTokens *uint64
}

func aggregateTokenUsage(query runtimehistory.Query, raw []rawTokenUsage) ([]runtimehistory.TokenUsagePoint, error) {
	latest := map[int]*rawTokenUsage{}
	for _, usage := range raw {
		index := bucketIndex(query, usage.resolvedAt)
		if index < 0 {
			continue
		}
		previous, ok := latest[index]
		if !ok || usage.resolvedAt.After(previous.resolvedAt) {
			value := usage
			latest[index] = &value
		}
	}
	result := make([]runtimehistory.TokenUsagePoint, 0, len(latest))
	for _, index := range sortedIndexes(latest) {
		usage := latest[index]
		start, end := bucketBounds(query, index)
		result = append(result, runtimehistory.TokenUsagePoint{
			Start: start, End: end, SampledAt: usage.resolvedAt,
			InputTokens: *usage.inputTokens, OutputTokens: *usage.outputTokens,
		})
	}
	return result, nil
}

type coverageAggregate struct {
	first, last           time.Time
	observations          int
	observed, unavailable int
}

type pointAggregate struct {
	coverageAggregate
	cpuContributors, memoryContributors int
	cpuUsageDelta, cpuCapacitySeconds   float64
	cpuCapacity                         *float64
	memoryUsage, memoryLimit            *uint64
}

type seriesAggregate struct {
	allocation string
	startedAt  time.Time
	provider   string
	samples    []*rawSample
}

func aggregate(query runtimehistory.Query, generatedAt time.Time, raw []*rawSample) (runtimehistory.Result, error) {
	coverage := map[int]*coverageAggregate{}
	series := map[string]*seriesAggregate{}
	for _, sample := range raw {
		if !sample.hasSample {
			continue
		}
		if !sample.resolvedAt.Before(query.Start) {
			index := bucketIndex(query, sample.resolvedAt)
			if index >= 0 {
				value := coverage[index]
				if value == nil {
					value = &coverageAggregate{}
					coverage[index] = value
				}
				addCoverage(value, sample)
			}
		}
		if sample.allocation == "" || sample.startedAt == nil || sample.provider == "" {
			continue
		}
		parsedAllocation, allocationErr := uuid.Parse(sample.allocation)
		if allocationErr != nil || parsedAllocation == uuid.Nil || parsedAllocation.String() != sample.allocation {
			return runtimehistory.Result{}, errors.New("invalid Runtime history allocation")
		}
		key := sample.allocation
		value := series[key]
		if value == nil {
			value = &seriesAggregate{allocation: sample.allocation, startedAt: *sample.startedAt, provider: sample.provider}
			series[key] = value
		} else if value.provider != sample.provider {
			return runtimehistory.Result{}, errors.New("conflicting Runtime history provider")
		} else if sample.startedAt.Before(value.startedAt) {
			value.startedAt = *sample.startedAt
		}
		value.samples = append(value.samples, sample)
	}
	// Unavailable samples retain allocation identity even without a provider
	// timestamp. Keep them in that allocation's coverage and rate sequence.
	for _, sample := range raw {
		if sample.startedAt == nil && sample.allocation != "" {
			if value := series[sample.allocation]; value != nil && !sample.resolvedAt.Before(value.startedAt) {
				value.samples = append(value.samples, sample)
			}
		}
	}
	result := runtimehistory.Result{GeneratedAt: generatedAt, Coverage: make([]runtimehistory.CoveragePoint, 0, len(coverage)), Series: make([]runtimehistory.Series, 0, len(series))}
	for _, index := range sortedIndexes(coverage) {
		value := coverage[index]
		start, end := bucketBounds(query, index)
		result.Coverage = append(result.Coverage, runtimehistory.CoveragePoint{
			Start: start, End: end, FirstObservedAt: value.first, LastObservedAt: value.last,
			ObservationCount: value.observations, ObservedCount: value.observed, UnavailableCount: value.unavailable,
		})
	}
	keys := make([]string, 0, len(series))
	for key := range series {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	totalPoints := len(result.Coverage)
	for _, key := range keys {
		value := series[key]
		points, err := aggregateSeries(query, value.samples)
		if err != nil {
			return runtimehistory.Result{}, err
		}
		if len(points) == 0 {
			continue
		}
		if len(result.Series) >= query.MaximumSeries {
			return runtimehistory.Result{}, errors.New("Runtime history series limit exceeded")
		}
		totalPoints += len(points)
		if len(points) > query.MaxPoints || totalPoints > query.MaximumTotalPoints {
			return runtimehistory.Result{}, errors.New("Runtime history point limit exceeded")
		}
		result.Series = append(result.Series, runtimehistory.Series{
			Scope: query.Scope, AllocationID: value.allocation, StartedAt: value.startedAt, ProviderType: value.provider, Points: points,
		})
	}
	return result, nil
}

func aggregateSeries(query runtimehistory.Query, samples []*rawSample) ([]runtimehistory.Point, error) {
	samples = append([]*rawSample(nil), samples...)
	sort.SliceStable(samples, func(i, j int) bool {
		left, right := samples[i].resolvedAt, samples[j].resolvedAt
		if samples[i].observedAt != nil {
			left = *samples[i].observedAt
		}
		if samples[j].observedAt != nil {
			right = *samples[j].observedAt
		}
		if left.Equal(right) {
			return samples[i].resolvedAt.Before(samples[j].resolvedAt)
		}
		return left.Before(right)
	})
	points := map[int]*pointAggregate{}
	var previousUsage, previousCapacity *float64
	var previousAt time.Time
	var previousStartedAt *time.Time
	for _, sample := range samples {
		if !sameOptionalTime(previousStartedAt, sample.startedAt) {
			previousUsage, previousCapacity = nil, nil
			previousAt = time.Time{}
		}
		previousStartedAt = sample.startedAt
		usage, hasUsage := sample.metrics[CPUUsageName]
		capacity, hasCapacity := sample.metrics[CPUCapacityName]
		if hasCapacity && capacity <= 0 {
			return nil, errors.New("invalid Runtime history CPU capacity")
		}
		if sample.hasSample && !sample.resolvedAt.Before(query.Start) {
			index := bucketIndex(query, sample.resolvedAt)
			if index >= 0 {
				point := points[index]
				if point == nil {
					point = &pointAggregate{}
					points[index] = point
				}
				addCoverage(&point.coverageAggregate, sample)
				cpuContributed := false
				if hasCapacity {
					value := capacity
					point.cpuCapacity = &value
					cpuContributed = true
				}
				if hasUsage && hasCapacity && sample.observedAt != nil && previousUsage != nil && previousCapacity != nil && !previousAt.IsZero() && sample.observedAt.After(previousAt) && usage >= *previousUsage {
					point.cpuUsageDelta += usage - *previousUsage
					point.cpuCapacitySeconds += sample.observedAt.Sub(previousAt).Seconds() * capacity
					cpuContributed = true
				}
				if cpuContributed {
					point.cpuContributors++
				}
				memoryContributed := false
				if rawValue, ok := sample.metrics[MemoryUsageName]; ok {
					value, err := safeUint64(rawValue)
					if err != nil {
						return nil, err
					}
					point.memoryUsage = &value
					memoryContributed = true
				}
				if rawValue, ok := sample.metrics[MemoryLimitName]; ok {
					value, err := safeUint64(rawValue)
					if err != nil || value == 0 {
						return nil, errors.New("invalid Runtime history memory limit")
					}
					point.memoryLimit = &value
					memoryContributed = true
				}
				if memoryContributed {
					point.memoryContributors++
				}
			}
		}
		if !hasUsage || !hasCapacity {
			previousUsage, previousCapacity = nil, nil
			previousAt = time.Time{}
		}
		if hasUsage {
			value := usage
			previousUsage = &value
			if sample.observedAt != nil {
				previousAt = *sample.observedAt
			} else {
				previousAt = time.Time{}
			}
			if hasCapacity {
				value := capacity
				previousCapacity = &value
			} else {
				previousCapacity = nil
			}
		}
	}
	result := make([]runtimehistory.Point, 0, len(points))
	for _, index := range sortedIndexes(points) {
		value := points[index]
		start, end := bucketBounds(query, index)
		point := runtimehistory.Point{
			Start: start, End: end, FirstObservedAt: value.first, LastObservedAt: value.last,
			ObservationCount: value.observations, ObservedCount: value.observed, UnavailableCount: value.unavailable,
			CPUContributorCount: value.cpuContributors, MemoryContributorCount: value.memoryContributors,
			CPUCapacityCores: value.cpuCapacity, MemoryUsageBytes: value.memoryUsage, MemoryLimitBytes: value.memoryLimit,
		}
		if value.cpuCapacitySeconds > 0 {
			ratio := value.cpuUsageDelta / value.cpuCapacitySeconds
			point.CPUUtilizationRatio = &ratio
		}
		result = append(result, point)
	}
	return result, nil
}

func safeUint64(value float64) (uint64, error) {
	const maxSafeInteger = uint64(1<<53 - 1)
	if value < 0 || value > float64(maxSafeInteger) || math.Trunc(value) != value {
		return 0, errors.New("invalid Runtime history byte value")
	}
	return uint64(value), nil
}

func addCoverage(value *coverageAggregate, sample *rawSample) {
	value.observations++
	if sample.status == runtimeobs.StatusObserved {
		value.observed++
	} else {
		value.unavailable++
	}
	if value.first.IsZero() || sample.resolvedAt.Before(value.first) {
		value.first = sample.resolvedAt
	}
	if value.last.IsZero() || sample.resolvedAt.After(value.last) {
		value.last = sample.resolvedAt
	}
}

func bucketIndex(query runtimehistory.Query, value time.Time) int {
	if value.Before(query.Start) || !value.Before(query.End) {
		return -1
	}
	return int(value.Sub(query.Start) / query.Step)
}

func bucketBounds(query runtimehistory.Query, index int) (time.Time, time.Time) {
	start := query.Start.Add(time.Duration(index) * query.Step)
	end := start.Add(query.Step)
	if end.After(query.End) {
		end = query.End
	}
	return start, end
}

func sortedIndexes[T any](values map[int]*T) []int {
	result := make([]int, 0, len(values))
	for index := range values {
		result = append(result, index)
	}
	sort.Ints(result)
	return result
}

func sameOptionalTime(left, right *time.Time) bool {
	return left == nil && right == nil || left != nil && right != nil && left.Equal(*right)
}
