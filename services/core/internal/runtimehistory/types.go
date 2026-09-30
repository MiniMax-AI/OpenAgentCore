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
	MetricTokens Metric = "tokens"
)

var providerTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

const maxSafeInteger = uint64(1<<53 - 1)

func canonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil && parsed.String() == value
}

func validPublicTime(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	seconds := value.Unix()
	return seconds >= 0 && uint64(seconds) <= maxSafeInteger
}

func validPublicBoundary(value time.Time) bool {
	return validPublicTime(value) && value.Nanosecond() == 0
}

func validCount(value int) bool {
	return value >= 0 && uint64(value) <= maxSafeInteger
}

// Capabilities contains only backend-neutral facts safe to expose through a
// future public capability response. Backend names, endpoints, credentials and
// tenant identity are deliberately absent.
type Capabilities struct {
	CollectionMode     CollectionMode
	SampleInterval     time.Duration
	Retention          time.Duration
	MinimumStep        time.Duration
	MaximumRange       time.Duration
	MaximumPoints      int
	MaximumSeries      int
	MaximumTotalPoints int
	Metrics            []Metric
}

func (c Capabilities) Durable() bool {
	return c.CollectionMode == CollectionPeriodic && c.SampleInterval > 0
}

func (c Capabilities) Validate() error {
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
	for _, value := range []time.Duration{c.SampleInterval, c.Retention, c.MinimumStep, c.MaximumRange} {
		if value%time.Second != 0 {
			return errors.New("Runtime history public time bounds must use whole seconds")
		}
	}
	if c.MaximumPoints < 2 || c.MaximumPoints > 10_000 {
		return errors.New("invalid Runtime history point limit")
	}
	if c.MaximumSeries < 1 || c.MaximumSeries > 1_000 || c.MaximumTotalPoints < c.MaximumPoints || c.MaximumTotalPoints > 100_000 {
		return errors.New("invalid Runtime history result limits")
	}
	if len(c.Metrics) == 0 || len(c.Metrics) > 3 {
		return errors.New("invalid Runtime history metrics")
	}
	seen := map[Metric]bool{}
	for _, metric := range c.Metrics {
		if metric != MetricCPU && metric != MetricMemory && metric != MetricTokens || seen[metric] {
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
		if !canonicalUUID(value) {
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
	Start, End                        time.Time
	Step                              time.Duration
	Retention                         time.Duration
	MaxPoints                         int
	MaximumSeries, MaximumTotalPoints int
}

// Result is returned by a backend Reader before the service validates identity,
// ordering, bounds and values. Empty retained ranges and missing metric values
// remain explicit; a Reader must never synthesize zeroes for absent samples.
type Result struct {
	GeneratedAt  time.Time
	RetainedFrom *time.Time
	Coverage     []CoveragePoint
	Series       []Series
	TokenUsage   []TokenUsagePoint
}

// TokenUsagePoint is the final cumulative measured Session usage, active Turns
// included, in one bucket. Throughput is derived from ordered adjacent points by clients.
type TokenUsagePoint struct {
	Start, End                time.Time
	SampledAt                 time.Time
	InputTokens, OutputTokens uint64
}

type CoveragePoint struct {
	Start, End                      time.Time
	FirstObservedAt, LastObservedAt time.Time
	ObservationCount                int
	ObservedCount, UnavailableCount int
}

type Series struct {
	Scope
	AllocationID string
	StartedAt    time.Time
	ProviderType string
	Points       []Point
}

// Point represents one server-selected bucket. CPUUtilizationRatio is derived
// from ordered cumulative counters within this Series' allocation; a counter
// regression resets the rate baseline. A provider without cumulative CPU time
// (E2B) reports utilization directly, and the bucket holds the mean of those
// reports. Memory values are the final observed values in the bucket.
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
	Coverage     []CoveragePoint
	Series       []Series
	TokenUsage   []TokenUsagePoint
}

func (r Response) Validate(now time.Time) error {
	if err := r.Capabilities.Validate(); err != nil || r.Scope.validate() != nil {
		return ErrInvalidResult
	}
	if !validPublicBoundary(r.Requested.Start) || !validPublicBoundary(r.Requested.End) || !r.Requested.End.After(r.Requested.Start) || r.Requested.End.Sub(r.Requested.Start) > r.MaximumRange || r.Requested.MaxPoints < 2 || r.Requested.MaxPoints > r.MaximumPoints {
		return ErrInvalidResult
	}
	wantResolution := resolution(r.Requested.End.Sub(r.Requested.Start), r.Requested.MaxPoints, r.MinimumStep)
	if r.Resolution != wantResolution {
		return ErrInvalidResult
	}
	query := Query{
		Scope: r.Scope, Start: r.Requested.Start, End: r.Requested.End, Step: r.Resolution, Retention: r.Retention, MaxPoints: r.Requested.MaxPoints,
		MaximumSeries: r.MaximumSeries, MaximumTotalPoints: r.MaximumTotalPoints,
	}
	if err := validateResult(query, Result{GeneratedAt: r.GeneratedAt, RetainedFrom: r.RetainedFrom, Coverage: r.Coverage, Series: r.Series, TokenUsage: r.TokenUsage}, now); err != nil {
		return ErrInvalidResult
	}
	return nil
}

func validateResult(query Query, result Result, now time.Time) error {
	if query.Retention <= 0 {
		return errors.New("invalid Runtime history query retention")
	}
	if !validPublicTime(result.GeneratedAt) || result.GeneratedAt.Before(query.Start) || result.GeneratedAt.After(now.Add(time.Second)) {
		return errors.New("invalid Runtime history generation time")
	}
	if result.RetainedFrom != nil && (!validPublicBoundary(*result.RetainedFrom) || result.RetainedFrom.After(query.End) || result.RetainedFrom.After(result.GeneratedAt)) {
		return errors.New("invalid Runtime history retention boundary")
	}
	retainedStart := query.Start
	if configuredStart := result.GeneratedAt.Add(-query.Retention); configuredStart.After(retainedStart) {
		retainedStart = configuredStart
	}
	if result.RetainedFrom != nil && result.RetainedFrom.After(retainedStart) {
		retainedStart = *result.RetainedFrom
	}
	if len(result.Coverage) > query.MaxPoints {
		return errors.New("Runtime history result exceeds coverage point limit")
	}
	var coverageObservationCount uint64
	for index, point := range result.Coverage {
		if err := validateCoveragePoint(query, point); err != nil {
			return err
		}
		if uint64(point.ObservationCount) > maxSafeInteger-coverageObservationCount {
			return errors.New("Runtime history coverage sample count exceeds safe integer limit")
		}
		coverageObservationCount += uint64(point.ObservationCount)
		if point.LastObservedAt.After(result.GeneratedAt) {
			return errors.New("Runtime history coverage observation is newer than result")
		}
		if point.ObservationCount > 0 && point.FirstObservedAt.Before(retainedStart) {
			return errors.New("Runtime history coverage predates retention")
		}
		if index > 0 && result.Coverage[index-1].End.After(point.Start) {
			return errors.New("Runtime history coverage points overlap or are out of order")
		}
	}
	seriesKeys := map[string]bool{}
	totalPoints := len(result.Coverage)
	if len(result.Series) > query.MaximumSeries {
		return errors.New("Runtime history result exceeds series limit")
	}
	for index := range result.Series {
		series := &result.Series[index]
		if series.Scope != query.Scope || !canonicalUUID(series.AllocationID) || !validPublicTime(series.StartedAt) || series.StartedAt.After(result.GeneratedAt) || !series.StartedAt.Before(query.End) || !providerTypePattern.MatchString(series.ProviderType) {
			return errors.New("invalid Runtime history series identity")
		}
		key := series.AllocationID
		if seriesKeys[key] {
			return errors.New("duplicate Runtime history series")
		}
		seriesKeys[key] = true
		totalPoints += len(series.Points)
		if len(series.Points) > query.MaxPoints || totalPoints > query.MaximumTotalPoints {
			return errors.New("Runtime history result exceeds point limit")
		}
		for pointIndex := range series.Points {
			point := series.Points[pointIndex]
			if err := validatePoint(query, series.StartedAt, point); err != nil {
				return err
			}
			if point.LastObservedAt.After(result.GeneratedAt) {
				return errors.New("Runtime history observation is newer than result")
			}
			if point.ObservationCount > 0 && point.FirstObservedAt.Before(retainedStart) {
				return errors.New("Runtime history observation predates retention")
			}
			if pointIndex > 0 && series.Points[pointIndex-1].End.After(point.Start) {
				return errors.New("Runtime history points overlap or are out of order")
			}
		}
	}
	if len(result.TokenUsage) > query.MaxPoints {
		return errors.New("Runtime history result exceeds token point limit")
	}
	totalPoints += len(result.TokenUsage)
	if totalPoints > query.MaximumTotalPoints {
		return errors.New("Runtime history result exceeds point limit")
	}
	for index, point := range result.TokenUsage {
		if !validPublicBoundary(point.Start) || !validPublicBoundary(point.End) || point.Start.Before(query.Start) || !point.End.After(point.Start) || point.End.After(query.End) || point.End.Sub(point.Start) > query.Step ||
			!validPublicTime(point.SampledAt) || point.SampledAt.Before(point.Start) || !point.SampledAt.Before(point.End) || point.SampledAt.After(result.GeneratedAt) || point.SampledAt.Before(retainedStart) ||
			point.InputTokens > maxSafeInteger || point.OutputTokens > maxSafeInteger {
			return errors.New("invalid Runtime history token usage point")
		}
		if index > 0 && result.TokenUsage[index-1].End.After(point.Start) {
			return errors.New("Runtime history token usage points overlap or are out of order")
		}
	}
	return nil
}

func validateCoveragePoint(query Query, point CoveragePoint) error {
	if !validPublicBoundary(point.Start) || !validPublicBoundary(point.End) || point.Start.Before(query.Start) || !point.End.After(point.Start) || point.End.After(query.End) || point.End.Sub(point.Start) > query.Step {
		return errors.New("invalid Runtime history coverage bounds")
	}
	if !validCount(point.ObservationCount) || !validCount(point.ObservedCount) || !validCount(point.UnavailableCount) || uint64(point.ObservedCount)+uint64(point.UnavailableCount) != uint64(point.ObservationCount) {
		return errors.New("invalid Runtime history coverage count")
	}
	if point.ObservationCount == 0 {
		if !point.FirstObservedAt.IsZero() || !point.LastObservedAt.IsZero() {
			return errors.New("empty Runtime history coverage contains observations")
		}
		return nil
	}
	if !validPublicTime(point.FirstObservedAt) || !validPublicTime(point.LastObservedAt) || point.FirstObservedAt.Before(point.Start) || point.LastObservedAt.Before(point.FirstObservedAt) || !point.LastObservedAt.Before(point.End) {
		return errors.New("invalid Runtime history coverage observation bounds")
	}
	return nil
}

func validatePoint(query Query, startedAt time.Time, point Point) error {
	if !validPublicBoundary(point.Start) || !validPublicBoundary(point.End) || point.Start.Before(query.Start) || !point.End.After(point.Start) || point.End.After(query.End) || point.End.Sub(point.Start) > query.Step || !point.End.After(startedAt) {
		return errors.New("invalid Runtime history point bounds")
	}
	if !validCount(point.ObservationCount) || !validCount(point.ObservedCount) || !validCount(point.UnavailableCount) || uint64(point.ObservedCount)+uint64(point.UnavailableCount) != uint64(point.ObservationCount) ||
		!validCount(point.CPUContributorCount) || point.CPUContributorCount > point.ObservedCount || !validCount(point.MemoryContributorCount) || point.MemoryContributorCount > point.ObservedCount {
		return errors.New("invalid Runtime history point coverage")
	}
	if point.ObservationCount == 0 {
		if !point.FirstObservedAt.IsZero() || !point.LastObservedAt.IsZero() || point.CPUUtilizationRatio != nil || point.CPUCapacityCores != nil || point.MemoryUsageBytes != nil || point.MemoryLimitBytes != nil {
			return errors.New("empty Runtime history bucket contains observations")
		}
		return nil
	}
	if !validPublicTime(point.FirstObservedAt) || !validPublicTime(point.LastObservedAt) || point.FirstObservedAt.Before(point.Start) || point.FirstObservedAt.Before(startedAt) || point.LastObservedAt.Before(point.FirstObservedAt) || !point.LastObservedAt.Before(point.End) {
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
