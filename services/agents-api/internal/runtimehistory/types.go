package runtimehistory

import (
	"errors"
	"math"
	"regexp"
	"slices"
	"time"

	"github.com/google/uuid"
)

type CollectionMode string

const (
	CollectionOnRead   CollectionMode = "on_read"
	CollectionPeriodic CollectionMode = "periodic"
)

type Metric string

const (
	MetricCPU    Metric = "cpu"
	MetricMemory Metric = "memory"
)

var providerTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// Capabilities contains only backend-neutral facts safe to expose through a
// future public capability response. Backend names, endpoints, credentials and
// tenant identity are deliberately absent.
type Capabilities struct {
	CollectionMode CollectionMode
	SampleInterval time.Duration
	Retention      time.Duration
	MinimumStep    time.Duration
	MaximumRange   time.Duration
	MaximumPoints  int
	Metrics        []Metric
}

func (c Capabilities) Durable() bool {
	return c.CollectionMode == CollectionPeriodic && c.SampleInterval > 0
}

func (c Capabilities) validate() error {
	switch c.CollectionMode {
	case CollectionOnRead:
		if c.SampleInterval != 0 {
			return errors.New("on-read Runtime history cannot declare a sampling interval")
		}
	case CollectionPeriodic:
		if c.SampleInterval <= 0 {
			return errors.New("periodic Runtime history requires a sampling interval")
		}
	default:
		return errors.New("invalid Runtime history collection mode")
	}
	if c.Retention <= 0 || c.MinimumStep <= 0 || c.MaximumRange <= 0 || c.MaximumRange > c.Retention {
		return errors.New("invalid Runtime history time bounds")
	}
	if c.MaximumPoints < 2 || c.MaximumPoints > 10_000 {
		return errors.New("invalid Runtime history point limit")
	}
	if len(c.Metrics) == 0 || len(c.Metrics) > 2 {
		return errors.New("invalid Runtime history metrics")
	}
	seen := map[Metric]bool{}
	for _, metric := range c.Metrics {
		if metric != MetricCPU && metric != MetricMemory || seen[metric] {
			return errors.New("invalid Runtime history metrics")
		}
		seen[metric] = true
	}
	return nil
}

type Scope struct {
	TenantID, SessionID, EnvironmentID string
}

func (s Scope) validate() error {
	for _, value := range []string{s.TenantID, s.SessionID, s.EnvironmentID} {
		if uuid.Validate(value) != nil {
			return errors.New("invalid Runtime history scope")
		}
	}
	return nil
}

type Range struct {
	Start, End time.Time
	MaxPoints  int
}

type Query struct {
	Scope
	Start, End time.Time
	Step       time.Duration
	MaxPoints  int
}

// Result is returned by a backend Reader before the service validates identity,
// ordering, bounds and values. Empty retained ranges and missing metric values
// remain explicit; a Reader must never synthesize zeroes for absent samples.
type Result struct {
	GeneratedAt  time.Time
	RetainedFrom *time.Time
	Series       []Series
}

type Series struct {
	Scope
	AllocationID string
	StartedAt    time.Time
	ProviderType string
	Points       []Point
}

// Point represents one server-selected bucket. CPUUtilizationRatio is derived
// only from ordered cumulative counters within this Series' allocation and
// StartedAt fence. Memory values are the final observed values in the bucket.
type Point struct {
	Start, End                            time.Time
	FirstObservedAt, LastObservedAt       time.Time
	ObservationCount                      int
	ObservedCount, UnavailableCount       int
	CPUContributorCount                   int
	MemoryContributorCount                int
	CPUUtilizationRatio, CPUCapacityCores *float64
	MemoryUsageBytes, MemoryLimitBytes    *uint64
}

type Response struct {
	Capabilities
	Scope
	Requested    Range
	Resolution   time.Duration
	GeneratedAt  time.Time
	RetainedFrom *time.Time
	Series       []Series
}

func validateResult(query Query, result Result, now time.Time) error {
	if result.GeneratedAt.IsZero() || result.GeneratedAt.After(now.Add(time.Second)) {
		return errors.New("invalid Runtime history generation time")
	}
	if result.RetainedFrom != nil && (result.RetainedFrom.IsZero() || result.RetainedFrom.After(query.End)) {
		return errors.New("invalid Runtime history retention boundary")
	}
	seriesKeys := map[string]bool{}
	totalPoints := 0
	if len(result.Series) > query.MaxPoints {
		return errors.New("Runtime history result exceeds series limit")
	}
	for index := range result.Series {
		series := &result.Series[index]
		if series.Scope != query.Scope || uuid.Validate(series.AllocationID) != nil || series.StartedAt.IsZero() || !series.StartedAt.Before(query.End) || !providerTypePattern.MatchString(series.ProviderType) {
			return errors.New("invalid Runtime history series identity")
		}
		key := series.AllocationID + "\x00" + series.StartedAt.UTC().Format(time.RFC3339Nano)
		if seriesKeys[key] {
			return errors.New("duplicate Runtime history series")
		}
		seriesKeys[key] = true
		totalPoints += len(series.Points)
		if totalPoints > query.MaxPoints {
			return errors.New("Runtime history result exceeds point limit")
		}
		for pointIndex := range series.Points {
			point := series.Points[pointIndex]
			if err := validatePoint(query, series.StartedAt, point); err != nil {
				return err
			}
			if pointIndex > 0 && series.Points[pointIndex-1].End.After(point.Start) {
				return errors.New("Runtime history points overlap or are out of order")
			}
		}
	}
	return nil
}

func validatePoint(query Query, startedAt time.Time, point Point) error {
	if point.Start.Before(query.Start) || !point.End.After(point.Start) || point.End.After(query.End) || point.End.Sub(point.Start) > query.Step || point.End.Before(startedAt) {
		return errors.New("invalid Runtime history point bounds")
	}
	if point.ObservationCount < 0 || point.ObservedCount < 0 || point.UnavailableCount < 0 || point.ObservedCount+point.UnavailableCount != point.ObservationCount ||
		point.CPUContributorCount < 0 || point.CPUContributorCount > point.ObservedCount || point.MemoryContributorCount < 0 || point.MemoryContributorCount > point.ObservedCount {
		return errors.New("invalid Runtime history point coverage")
	}
	if point.ObservationCount == 0 {
		if !point.FirstObservedAt.IsZero() || !point.LastObservedAt.IsZero() || point.CPUUtilizationRatio != nil || point.CPUCapacityCores != nil || point.MemoryUsageBytes != nil || point.MemoryLimitBytes != nil {
			return errors.New("empty Runtime history bucket contains observations")
		}
		return nil
	}
	if point.FirstObservedAt.Before(point.Start) || point.FirstObservedAt.Before(startedAt) || point.LastObservedAt.Before(point.FirstObservedAt) || !point.LastObservedAt.Before(point.End) {
		return errors.New("invalid Runtime history observation bounds")
	}
	if value := point.CPUUtilizationRatio; value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
		return errors.New("invalid Runtime history CPU utilization")
	}
	if value := point.CPUCapacityCores; value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value <= 0) {
		return errors.New("invalid Runtime history CPU capacity")
	}
	if point.CPUContributorCount == 0 && (point.CPUUtilizationRatio != nil || point.CPUCapacityCores != nil) {
		return errors.New("Runtime history CPU value lacks coverage")
	}
	if point.CPUContributorCount > 0 && point.CPUUtilizationRatio == nil && point.CPUCapacityCores == nil {
		return errors.New("Runtime history CPU coverage lacks a value")
	}
	const maxSafeInteger = uint64(1<<53 - 1)
	if point.MemoryUsageBytes != nil && *point.MemoryUsageBytes > maxSafeInteger || point.MemoryLimitBytes != nil && (*point.MemoryLimitBytes == 0 || *point.MemoryLimitBytes > maxSafeInteger) {
		return errors.New("invalid Runtime history memory value")
	}
	if point.MemoryContributorCount == 0 && (point.MemoryUsageBytes != nil || point.MemoryLimitBytes != nil) {
		return errors.New("Runtime history memory value lacks coverage")
	}
	if point.MemoryContributorCount > 0 && point.MemoryUsageBytes == nil && point.MemoryLimitBytes == nil {
		return errors.New("Runtime history memory coverage lacks a value")
	}
	return nil
}

func cloneCapabilities(value Capabilities) Capabilities {
	value.Metrics = slices.Clone(value.Metrics)
	return value
}
