package postgresreader

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/google/uuid"
)

// This bound accommodates a full 24 hours at five-second cadence, independently
// of the requested chart resolution, including the CPU lookback.
const maximumRawSamples = 20_000
const retention = 7 * 24 * time.Hour

type sampleStore interface {
	InsertRuntimeHistorySample(context.Context, runtimeobs.ExportRecord) error
	ListRuntimeHistorySamples(context.Context, string, string, string, int64, int64, int32) ([]runtimeobs.ExportRecord, error)
	PruneRuntimeHistorySamples(context.Context, int64) (int64, error)
}

type Config struct {
	Capabilities runtimehistory.Capabilities
	QueryTimeout time.Duration
}

type Reader struct {
	store        sampleStore
	capabilities runtimehistory.Capabilities
	queryTimeout time.Duration
	now          func() time.Time
}

func New(store sampleStore, config Config) (*Reader, error) {
	if store == nil || config.QueryTimeout <= 0 || config.Capabilities.Validate() != nil || config.Capabilities.Retention != retention || config.Capabilities.MaximumRange > 24*time.Hour {
		return nil, errors.New("invalid Runtime history PostgreSQL configuration")
	}
	capabilities := config.Capabilities
	capabilities.Metrics = append([]runtimehistory.Metric(nil), capabilities.Metrics...)
	return &Reader{store: store, capabilities: capabilities, queryTimeout: config.QueryTimeout, now: time.Now}, nil
}

func (r *Reader) Capabilities() runtimehistory.Capabilities {
	value := r.capabilities
	value.Metrics = append([]runtimehistory.Metric(nil), value.Metrics...)
	return value
}

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
	if oldest := generatedAt.Add(-retention); rawStart.Before(oldest) {
		rawStart = oldest
	}
	if rawStart.UnixNano() < 0 {
		rawStart = time.Unix(0, 0).UTC()
	}
	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()
	records, err := r.store.ListRuntimeHistorySamples(queryCtx, query.TenantID, query.SessionID, query.EnvironmentID, rawStart.UnixNano(), query.End.UnixNano(), maximumRawSamples+1)
	if err != nil {
		return runtimehistory.Result{}, errors.New("query Runtime history")
	}
	if len(records) > maximumRawSamples {
		return runtimehistory.Result{}, errors.New("Runtime history raw result exceeds limit")
	}
	raw := make([]*rawSample, 0, len(records))
	tokens := make([]rawTokenUsage, 0, len(records))
	for _, record := range records {
		if validateRecord(record) != nil || record.TenantID != query.TenantID || record.SessionID != query.SessionID || record.EnvironmentID != query.EnvironmentID || record.ResolvedAt.Before(rawStart) || !record.ResolvedAt.Before(query.End) {
			return runtimehistory.Result{}, errors.New("invalid Runtime history stored sample")
		}
		sample := &rawSample{resolvedAt: record.ResolvedAt, allocation: record.AllocationID, provider: record.ProviderType, status: record.Status, hasSample: true, metrics: map[string]float64{}}
		if value := record.Sample; value != nil {
			sample.observedAt = &value.ObservedAt
			sample.startedAt = value.StartedAt
			if value.CPUUsageSecondsTotal != nil {
				sample.metrics[CPUUsageName] = *value.CPUUsageSecondsTotal
			}
			if value.CPUCapacityCores != nil {
				sample.metrics[CPUCapacityName] = *value.CPUCapacityCores
			}
			if value.MemoryUsageBytes != nil {
				sample.metrics[MemoryUsageName] = float64(*value.MemoryUsageBytes)
			}
			if value.MemoryLimitBytes != nil {
				sample.metrics[MemoryLimitName] = float64(*value.MemoryLimitBytes)
			}
		}
		raw = append(raw, sample)
		if value := record.TokenUsage; value != nil && !record.ResolvedAt.Before(query.Start) {
			tokens = append(tokens, rawTokenUsage{resolvedAt: record.ResolvedAt, inputTokens: &value.InputTokens, outputTokens: &value.OutputTokens})
		}
	}
	result, err := aggregate(query, generatedAt, raw)
	if err != nil {
		return runtimehistory.Result{}, err
	}
	for _, metric := range r.capabilities.Metrics {
		if metric == runtimehistory.MetricTokens {
			result.TokenUsage, err = aggregateTokenUsage(query, tokens)
			break
		}
	}
	return result, err
}

// Prune expires old telemetry even when no Runtime is being sampled. Each call
// deletes at most 4,096 rows in small lock-skipping batches under one deadline.
func (r *Reader) Prune(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()
	before := r.now().Add(-retention).UnixNano()
	var removed int64
	for range 16 {
		count, err := r.store.PruneRuntimeHistorySamples(ctx, before)
		if err != nil {
			return removed, errors.New("prune Runtime history")
		}
		removed += count
		if count < 256 {
			return removed, nil
		}
	}
	return removed, nil
}

func (r *Reader) validateQuery(query runtimehistory.Query) error {
	for _, value := range []string{query.TenantID, query.SessionID, query.EnvironmentID} {
		parsed, err := uuid.Parse(value)
		if err != nil || parsed == uuid.Nil || parsed.String() != value {
			return errors.New("invalid Runtime history PostgreSQL scope")
		}
	}
	if query.Start.IsZero() || query.End.IsZero() || !query.End.After(query.Start) || query.Start.Nanosecond() != 0 || query.End.Nanosecond() != 0 ||
		query.Step < r.capabilities.MinimumStep || query.Step%time.Second != 0 || query.Retention != r.capabilities.Retention ||
		query.MaxPoints < 2 || query.MaxPoints > r.capabilities.MaximumPoints || query.MaximumSeries != r.capabilities.MaximumSeries ||
		query.MaximumTotalPoints != r.capabilities.MaximumTotalPoints || query.End.Sub(query.Start) > r.capabilities.MaximumRange {
		return errors.New("invalid Runtime history PostgreSQL query")
	}
	buckets := query.End.Sub(query.Start) / query.Step
	if query.End.Sub(query.Start)%query.Step != 0 {
		buckets++
	}
	if buckets > time.Duration(query.MaxPoints) {
		return errors.New("Runtime history PostgreSQL query exceeds point budget")
	}
	return nil
}
