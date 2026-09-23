// Package clickhousereader implements the optional Runtime history Reader
// against the operator-owned ClickHouse projection documented with this
// repository. It is a read-only adapter and is never lifecycle authority.
package clickhousereader

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net"
	"sort"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs/otlpexporter"
	"github.com/google/uuid"
)

const historyQuery = `
SELECT
    resolved_at_unix_nano,
    observed_at_unix_nano,
    allocation_id,
    started_at_unix_nano,
    provider_type,
    status,
    metric_name,
    min(value) AS minimum_value,
    max(value) AS maximum_value
FROM runtime_history_metrics
WHERE tenant_id = {tenant_id:String}
  AND session_id = {session_id:String}
  AND environment_id = {environment_id:String}
  AND collection_source = 'periodic'
  AND resolved_at_unix_nano >= {raw_start:Int64}
  AND resolved_at_unix_nano < {end:Int64}
  AND metric_name IN (
      'agents.runtime.sample',
      'agents.runtime.cpu.usage',
      'agents.runtime.cpu.capacity',
      'agents.runtime.memory.usage',
      'agents.runtime.memory.limit'
  )
GROUP BY
    resolved_at_unix_nano,
    observed_at_unix_nano,
    allocation_id,
    started_at_unix_nano,
    provider_type,
    status,
    metric_name
ORDER BY resolved_at_unix_nano, allocation_id, started_at_unix_nano, metric_name
LIMIT {row_limit:UInt64}`

const tokenUsageQuery = `
SELECT
    resolved_at_unix_nano,
    metric_name,
    min(value) AS minimum_value,
    max(value) AS maximum_value
FROM runtime_history_metrics
WHERE tenant_id = {tenant_id:String}
  AND session_id = {session_id:String}
  AND environment_id = {environment_id:String}
  AND collection_source = 'periodic'
  AND resolved_at_unix_nano >= {raw_start:Int64}
  AND resolved_at_unix_nano < {end:Int64}
  AND metric_name IN (
      'agents.session.tokens.input',
      'agents.session.tokens.output'
  )
GROUP BY resolved_at_unix_nano, metric_name
ORDER BY resolved_at_unix_nano, metric_name
LIMIT {row_limit:UInt64}`

type Config struct {
	Address      string
	Database     string
	Username     string
	Password     string
	Secure       bool
	Insecure     bool
	DialTimeout  time.Duration
	QueryTimeout time.Duration
	Capabilities runtimehistory.Capabilities
}

type rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}

type queryClient interface {
	Query(context.Context, string, ...any) (rows, error)
	Close() error
}

type sqlClient struct{ db *sql.DB }

func (c sqlClient) Query(ctx context.Context, statement string, args ...any) (rows, error) {
	return c.db.QueryContext(ctx, statement, args...)
}

func (c sqlClient) Close() error { return c.db.Close() }

type Reader struct {
	client       queryClient
	capabilities runtimehistory.Capabilities
	queryTimeout time.Duration
	now          func() time.Time
}

func Open(_ context.Context, config Config) (*Reader, error) {
	host, _, err := net.SplitHostPort(config.Address)
	if err != nil || host == "" || config.Database == "" || config.Username == "" || config.Secure == config.Insecure || config.DialTimeout <= 0 || config.QueryTimeout <= 0 {
		return nil, errors.New("invalid Runtime history ClickHouse configuration")
	}
	if err := config.Capabilities.Validate(); err != nil {
		return nil, errors.New("invalid Runtime history ClickHouse capabilities")
	}
	options := &clickhouse.Options{
		Addr:            []string{config.Address},
		Auth:            clickhouse.Auth{Database: config.Database, Username: config.Username, Password: config.Password},
		DialTimeout:     config.DialTimeout,
		MaxOpenConns:    4,
		MaxIdleConns:    2,
		ConnMaxLifetime: 10 * time.Minute,
	}
	if config.Secure {
		options.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}
	}
	db := clickhouse.OpenDB(options)
	return newReader(sqlClient{db: db}, config.Capabilities, config.QueryTimeout)
}

func newReader(client queryClient, capabilities runtimehistory.Capabilities, queryTimeout time.Duration) (*Reader, error) {
	if client == nil || queryTimeout <= 0 || capabilities.Validate() != nil {
		return nil, errors.New("invalid Runtime history ClickHouse Reader")
	}
	return &Reader{client: client, capabilities: capabilities, queryTimeout: queryTimeout, now: time.Now}, nil
}

func (r *Reader) Capabilities() runtimehistory.Capabilities {
	value := r.capabilities
	value.Metrics = append([]runtimehistory.Metric(nil), value.Metrics...)
	return value
}

func (r *Reader) Close() error { return r.client.Close() }

func (r *Reader) Query(ctx context.Context, query runtimehistory.Query) (runtimehistory.Result, error) {
	if err := r.validateQuery(query); err != nil {
		return runtimehistory.Result{}, err
	}
	generatedAt := r.now().UTC()
	lookback := r.capabilities.MinimumStep
	if r.capabilities.SampleInterval > 0 {
		lookback = 2 * r.capabilities.SampleInterval
	}
	rawStart := query.Start.Add(-lookback)
	retentionStart := generatedAt.Add(-r.capabilities.Retention)
	if rawStart.Before(retentionStart) {
		rawStart = retentionStart
	}
	if rawStart.UnixNano() < 0 {
		rawStart = time.Unix(0, 0).UTC()
	}
	rowLimit := query.MaximumTotalPoints*6 + query.MaximumSeries*6 + 1
	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()
	resultRows, err := r.client.Query(queryCtx, historyQuery,
		clickhouse.Named("tenant_id", query.TenantID),
		clickhouse.Named("session_id", query.SessionID),
		clickhouse.Named("environment_id", query.EnvironmentID),
		clickhouse.Named("raw_start", rawStart.UnixNano()),
		clickhouse.Named("end", query.End.UnixNano()),
		clickhouse.Named("row_limit", uint64(rowLimit)),
	)
	if err != nil {
		return runtimehistory.Result{}, errors.New("query Runtime history")
	}
	defer resultRows.Close()
	raw, count, err := readSamples(resultRows, rawStart, query.End)
	if err != nil {
		return runtimehistory.Result{}, err
	}
	if count >= rowLimit {
		return runtimehistory.Result{}, errors.New("Runtime history raw result exceeds limit")
	}
	result, err := aggregate(query, generatedAt, raw)
	if err != nil || !hasMetric(r.capabilities.Metrics, runtimehistory.MetricTokens) {
		return result, err
	}
	tokenRowLimit := query.MaximumTotalPoints*2 + 1
	tokenRows, err := r.client.Query(queryCtx, tokenUsageQuery,
		clickhouse.Named("tenant_id", query.TenantID),
		clickhouse.Named("session_id", query.SessionID),
		clickhouse.Named("environment_id", query.EnvironmentID),
		clickhouse.Named("raw_start", query.Start.UnixNano()),
		clickhouse.Named("end", query.End.UnixNano()),
		clickhouse.Named("row_limit", uint64(tokenRowLimit)),
	)
	if err != nil {
		return runtimehistory.Result{}, errors.New("query Runtime history token usage")
	}
	defer tokenRows.Close()
	rawTokens, tokenCount, err := readTokenUsage(tokenRows, query.Start, query.End)
	if err != nil {
		return runtimehistory.Result{}, err
	}
	if tokenCount >= tokenRowLimit {
		return runtimehistory.Result{}, errors.New("Runtime history token result exceeds limit")
	}
	result.TokenUsage, err = aggregateTokenUsage(query, rawTokens)
	return result, err
}

func hasMetric(metrics []runtimehistory.Metric, expected runtimehistory.Metric) bool {
	for _, metric := range metrics {
		if metric == expected {
			return true
		}
	}
	return false
}

func (r *Reader) validateQuery(query runtimehistory.Query) error {
	for _, value := range []string{query.TenantID, query.SessionID, query.EnvironmentID} {
		parsed, err := uuid.Parse(value)
		if err != nil || parsed == uuid.Nil || parsed.String() != value {
			return errors.New("invalid Runtime history ClickHouse scope")
		}
	}
	if query.Start.IsZero() || query.End.IsZero() || !query.End.After(query.Start) || query.Start.Nanosecond() != 0 || query.End.Nanosecond() != 0 ||
		query.Step < r.capabilities.MinimumStep || query.Step%time.Second != 0 || query.Retention != r.capabilities.Retention ||
		query.MaxPoints < 2 || query.MaxPoints > r.capabilities.MaximumPoints || query.MaximumSeries != r.capabilities.MaximumSeries ||
		query.MaximumTotalPoints != r.capabilities.MaximumTotalPoints || query.End.Sub(query.Start) > r.capabilities.MaximumRange {
		return errors.New("invalid Runtime history ClickHouse query")
	}
	buckets := query.End.Sub(query.Start) / query.Step
	if query.End.Sub(query.Start)%query.Step != 0 {
		buckets++
	}
	if buckets > time.Duration(query.MaxPoints) {
		return errors.New("Runtime history ClickHouse query exceeds point budget")
	}
	return nil
}

type nullableInt64 struct {
	value int64
	valid bool
}

func (n *nullableInt64) Scan(value any) error {
	if value == nil {
		n.valid = false
		return nil
	}
	switch typed := value.(type) {
	case int64:
		n.value, n.valid = typed, true
	case uint64:
		if typed > math.MaxInt64 {
			return errors.New("Runtime history timestamp is out of range")
		}
		n.value, n.valid = int64(typed), true
	default:
		return fmt.Errorf("invalid Runtime history timestamp type %T", value)
	}
	return nil
}

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

func readTokenUsage(resultRows rows, start, end time.Time) ([]rawTokenUsage, int, error) {
	byResolved := map[int64]*rawTokenUsage{}
	rowCount := 0
	for resultRows.Next() {
		rowCount++
		var resolvedNano int64
		var metric string
		var minimum, maximum float64
		if err := resultRows.Scan(&resolvedNano, &metric, &minimum, &maximum); err != nil {
			return nil, rowCount, errors.New("scan Runtime history token row")
		}
		resolvedAt := time.Unix(0, resolvedNano).UTC()
		value, err := safeUint64(minimum)
		if resolvedNano < 0 || resolvedAt.Before(start) || !resolvedAt.Before(end) || minimum != maximum || err != nil {
			return nil, rowCount, errors.New("invalid Runtime history token row")
		}
		usage := byResolved[resolvedNano]
		if usage == nil {
			usage = &rawTokenUsage{resolvedAt: resolvedAt}
			byResolved[resolvedNano] = usage
		}
		switch metric {
		case otlpexporter.TokenInputName:
			if usage.inputTokens != nil {
				return nil, rowCount, errors.New("duplicate Runtime history input token row")
			}
			usage.inputTokens = &value
		case otlpexporter.TokenOutputName:
			if usage.outputTokens != nil {
				return nil, rowCount, errors.New("duplicate Runtime history output token row")
			}
			usage.outputTokens = &value
		default:
			return nil, rowCount, errors.New("invalid Runtime history token metric")
		}
	}
	if err := resultRows.Err(); err != nil {
		return nil, rowCount, errors.New("iterate Runtime history token rows")
	}
	result := make([]rawTokenUsage, 0, len(byResolved))
	for _, usage := range byResolved {
		if usage.inputTokens == nil || usage.outputTokens == nil {
			return nil, rowCount, errors.New("incomplete Runtime history token usage")
		}
		result = append(result, *usage)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].resolvedAt.Before(result[j].resolvedAt) })
	return result, rowCount, nil
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

func readSamples(resultRows rows, rawStart, end time.Time) ([]*rawSample, int, error) {
	byKey := map[string]*rawSample{}
	rowCount := 0
	for resultRows.Next() {
		rowCount++
		var resolvedNano int64
		var observedNano, startedNano nullableInt64
		var allocation, provider, status, metric string
		var minimum, maximum float64
		if err := resultRows.Scan(&resolvedNano, &observedNano, &allocation, &startedNano, &provider, &status, &metric, &minimum, &maximum); err != nil {
			return nil, rowCount, errors.New("scan Runtime history row")
		}
		if resolvedNano < 0 || minimum != maximum || math.IsNaN(minimum) || math.IsInf(minimum, 0) || minimum < 0 {
			return nil, rowCount, errors.New("invalid Runtime history metric row")
		}
		resolvedAt := time.Unix(0, resolvedNano).UTC()
		if resolvedAt.Before(rawStart) || !resolvedAt.Before(end) {
			return nil, rowCount, errors.New("Runtime history row is outside the bounded query")
		}
		var observedAt *time.Time
		if observedNano.valid {
			value := time.Unix(0, observedNano.value).UTC()
			if observedNano.value < 0 || value.After(resolvedAt) {
				return nil, rowCount, errors.New("invalid Runtime history observed timestamp")
			}
			observedAt = &value
		}
		var startedAt *time.Time
		if startedNano.valid {
			value := time.Unix(0, startedNano.value).UTC()
			if startedNano.value < 0 || observedAt == nil || value.After(*observedAt) {
				return nil, rowCount, errors.New("invalid Runtime history incarnation timestamp")
			}
			startedAt = &value
		}
		parsedStatus := runtimeobs.Status(status)
		if parsedStatus != runtimeobs.StatusObserved && parsedStatus != runtimeobs.StatusUnavailable && parsedStatus != runtimeobs.StatusUnsupported {
			return nil, rowCount, errors.New("invalid Runtime history status")
		}
		if metric != otlpexporter.SampleName && metric != otlpexporter.CPUUsageName && metric != otlpexporter.CPUCapacityName && metric != otlpexporter.MemoryUsageName && metric != otlpexporter.MemoryLimitName {
			return nil, rowCount, errors.New("invalid Runtime history metric")
		}
		if err := validateProjectedRow(parsedStatus, metric, minimum, observedAt, startedAt); err != nil {
			return nil, rowCount, err
		}
		startedKey := ""
		if startedAt != nil {
			startedKey = fmt.Sprintf("%d", startedNano.value)
		}
		key := fmt.Sprintf("%d\x00%s\x00%s", resolvedNano, allocation, startedKey)
		sample := byKey[key]
		if sample == nil {
			sample = &rawSample{resolvedAt: resolvedAt, observedAt: observedAt, allocation: allocation, startedAt: startedAt, provider: provider, status: parsedStatus, metrics: map[string]float64{}}
			byKey[key] = sample
		} else if sample.provider != provider || sample.status != parsedStatus || !sameOptionalTime(sample.observedAt, observedAt) {
			return nil, rowCount, errors.New("conflicting Runtime history sample identity")
		}
		if previous, ok := sample.metrics[metric]; ok && previous != minimum {
			return nil, rowCount, errors.New("conflicting Runtime history metric value")
		}
		sample.metrics[metric] = minimum
		if metric == otlpexporter.SampleName {
			sample.hasSample = true
		}
	}
	if err := resultRows.Err(); err != nil {
		return nil, rowCount, errors.New("iterate Runtime history rows")
	}
	result := make([]*rawSample, 0, len(byKey))
	for _, sample := range byKey {
		if !sample.hasSample {
			return nil, rowCount, errors.New("Runtime history resource row has no sample marker")
		}
		result = append(result, sample)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].resolvedAt.Equal(result[j].resolvedAt) {
			if result[i].allocation == result[j].allocation {
				return timeValue(result[i].startedAt) < timeValue(result[j].startedAt)
			}
			return result[i].allocation < result[j].allocation
		}
		return result[i].resolvedAt.Before(result[j].resolvedAt)
	})
	return result, rowCount, nil
}

func validateProjectedRow(status runtimeobs.Status, metric string, value float64, observedAt, startedAt *time.Time) error {
	if metric == otlpexporter.SampleName && value != 1 {
		return errors.New("invalid Runtime history sample marker")
	}
	if status == runtimeobs.StatusObserved {
		if observedAt == nil {
			return errors.New("observed Runtime history row has no observation timestamp")
		}
		if metric != otlpexporter.SampleName && startedAt == nil {
			return errors.New("Runtime history resource metric has no incarnation fence")
		}
		return nil
	}
	if observedAt != nil || startedAt != nil || metric != otlpexporter.SampleName {
		return errors.New("unobserved Runtime history row carries sample data")
	}
	return nil
}

func sameOptionalTime(left, right *time.Time) bool {
	return left == nil && right == nil || left != nil && right != nil && left.Equal(*right)
}

func timeValue(value *time.Time) int64 {
	if value == nil {
		return -1
	}
	return value.UnixNano()
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
		key := sample.allocation + "\x00" + sample.startedAt.Format(time.RFC3339Nano)
		value := series[key]
		if value == nil {
			value = &seriesAggregate{allocation: sample.allocation, startedAt: *sample.startedAt, provider: sample.provider}
			series[key] = value
		} else if value.provider != sample.provider {
			return runtimehistory.Result{}, errors.New("conflicting Runtime history provider")
		}
		value.samples = append(value.samples, sample)
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
		if samples[i].observedAt == nil {
			return samples[j].observedAt != nil
		}
		if samples[j].observedAt == nil {
			return false
		}
		if samples[i].observedAt.Equal(*samples[j].observedAt) {
			return samples[i].resolvedAt.Before(samples[j].resolvedAt)
		}
		return samples[i].observedAt.Before(*samples[j].observedAt)
	})
	points := map[int]*pointAggregate{}
	var previousUsage, previousCapacity *float64
	var previousAt time.Time
	for _, sample := range samples {
		usage, hasUsage := sample.metrics[otlpexporter.CPUUsageName]
		capacity, hasCapacity := sample.metrics[otlpexporter.CPUCapacityName]
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
				if rawValue, ok := sample.metrics[otlpexporter.MemoryUsageName]; ok {
					value, err := safeUint64(rawValue)
					if err != nil {
						return nil, err
					}
					point.memoryUsage = &value
					memoryContributed = true
				}
				if rawValue, ok := sample.metrics[otlpexporter.MemoryLimitName]; ok {
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
