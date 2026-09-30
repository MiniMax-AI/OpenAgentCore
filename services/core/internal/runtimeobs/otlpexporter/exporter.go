// Package otlpexporter sends sanitized Runtime observation records to an
// operator-configured OTLP/HTTP metrics endpoint. It is optional history
// transport, not Runtime execution or lifecycle authority.
package otlpexporter

import (
	"context"
	"errors"
	"math"
	"regexp"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
)

const scopeName = "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs/otlpexporter"

const (
	CPUUsageName       = "agents.runtime.cpu.usage"
	CPUCapacityName    = "agents.runtime.cpu.capacity"
	CPUUtilizationName = "agents.runtime.cpu.utilization"
	MemoryUsageName    = "agents.runtime.memory.usage"
	MemoryLimitName    = "agents.runtime.memory.limit"
	SampleName         = "agents.runtime.sample"
	SampleDurationName = "agents.runtime.sample.duration"
	TokenInputName     = "agents.session.tokens.input"
	TokenOutputName    = "agents.session.tokens.output"
)

var safeLabelPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type Config struct {
	Endpoint       string
	Headers        map[string]string
	Insecure       bool
	RequestTimeout time.Duration
}

type metricClient interface {
	Export(context.Context, *metricdata.ResourceMetrics) error
	Shutdown(context.Context) error
}

type Exporter struct {
	client   metricClient
	resource *resource.Resource
}

func New(ctx context.Context, config Config) (*Exporter, error) {
	if config.Endpoint == "" {
		return nil, errors.New("Runtime history OTLP endpoint is required")
	}
	options := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(config.Endpoint),
		otlpmetrichttp.WithHeaders(config.Headers),
		otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{Enabled: true}),
	}
	if config.Insecure {
		options = append(options, otlpmetrichttp.WithInsecure())
	}
	if config.RequestTimeout > 0 {
		options = append(options, otlpmetrichttp.WithTimeout(config.RequestTimeout))
	}
	client, err := otlpmetrichttp.New(ctx, options...)
	if err != nil {
		return nil, errors.New("cannot initialize Runtime history OTLP exporter")
	}
	return newWithClient(client), nil
}

func newWithClient(client metricClient) *Exporter {
	return &Exporter{
		client: client,
		resource: resource.NewSchemaless(
			attribute.String("service.name", "oac-core"),
			attribute.String("service.namespace", "oac"),
		),
	}
}

func (e *Exporter) Export(ctx context.Context, record runtimeobs.ExportRecord) error {
	metrics, err := recordMetrics(record)
	if err != nil {
		return err
	}
	return e.client.Export(ctx, &metricdata.ResourceMetrics{
		Resource: e.resource,
		ScopeMetrics: []metricdata.ScopeMetrics{{
			Scope:   instrumentation.Scope{Name: scopeName},
			Metrics: metrics,
		}},
	})
}

func (e *Exporter) Close(ctx context.Context) error {
	return e.client.Shutdown(ctx)
}

func recordMetrics(record runtimeobs.ExportRecord) ([]metricdata.Metrics, error) {
	if err := validateRecord(record); err != nil {
		return nil, err
	}
	attributes := recordAttributes(record)
	result := []metricdata.Metrics{deltaCountMetric(SampleName, "Validated Runtime observation results", record.ResolvedAt, attributes)}
	if record.SourceDuration > 0 {
		result = append(result, durationMetric(record, attributes))
	}
	if record.TokenUsage != nil {
		result = append(result,
			integerGaugeMetric(TokenInputName, "Cumulative measured Session input tokens", "{token}", int64(record.TokenUsage.InputTokens), record.ResolvedAt, attributes),
			integerGaugeMetric(TokenOutputName, "Cumulative measured Session output tokens", "{token}", int64(record.TokenUsage.OutputTokens), record.ResolvedAt, attributes),
		)
	}
	if record.Sample == nil {
		return result, nil
	}

	sample := record.Sample
	// Resource values are meaningful only within a provider-qualified compute
	// incarnation. Keep the coverage result, but never let an unfenced sample
	// collapse CPU or memory points across Runtime restarts.
	if sample.StartedAt == nil {
		return result, nil
	}
	if sample.CPUUsageSecondsTotal != nil {
		result = append(result, cumulativeMetric(
			CPUUsageName, "Cumulative CPU time consumed by the Runtime incarnation", "s",
			*sample.CPUUsageSecondsTotal, sample.StartedAt, sample.ObservedAt, attributes,
		))
	}
	if sample.CPUCapacityCores != nil {
		result = append(result, gaugeMetric(CPUCapacityName, "Configured Runtime CPU capacity", "{core}", *sample.CPUCapacityCores, sample.ObservedAt, attributes))
	}
	if sample.CPUUtilizationRatio != nil {
		result = append(result, gaugeMetric(CPUUtilizationName, "Provider-reported share of Runtime CPU capacity", "1", *sample.CPUUtilizationRatio, sample.ObservedAt, attributes))
	}
	if sample.MemoryUsageBytes != nil {
		result = append(result, gaugeMetric(MemoryUsageName, "Current Runtime memory usage", "By", float64(*sample.MemoryUsageBytes), sample.ObservedAt, attributes))
	}
	if sample.MemoryLimitBytes != nil {
		result = append(result, gaugeMetric(MemoryLimitName, "Configured Runtime memory limit", "By", float64(*sample.MemoryLimitBytes), sample.ObservedAt, attributes))
	}
	return result, nil
}

func validateRecord(record runtimeobs.ExportRecord) error {
	if record.TenantID == "" || record.SessionID == "" || record.ResolvedAt.IsZero() || record.ResolvedAt.Unix() < 0 {
		return errors.New("invalid Runtime history export identity")
	}
	if record.ProviderType != "" && !safeLabelPattern.MatchString(record.ProviderType) {
		return errors.New("invalid Runtime history provider type")
	}
	if record.Reason != "" && !safeLabelPattern.MatchString(record.Reason) {
		return errors.New("invalid Runtime history result reason")
	}
	switch record.Mode {
	case runtimeobs.ModeNone, runtimeobs.ModeSelfHosted, runtimeobs.ModeManaged:
	default:
		return errors.New("invalid Runtime history mode")
	}
	switch record.Status {
	case runtimeobs.StatusObserved:
		if record.Sample == nil {
			return errors.New("observed Runtime history record has no sample")
		}
	case runtimeobs.StatusUnavailable, runtimeobs.StatusUnsupported:
		if record.Sample != nil {
			return errors.New("unobserved Runtime history record has a sample")
		}
	default:
		return errors.New("invalid Runtime history status")
	}
	switch record.CollectionSource {
	case runtimeobs.CollectionSourceOnRead, runtimeobs.CollectionSourcePeriodic:
	default:
		return errors.New("invalid Runtime history collection source")
	}
	if record.SourceDuration < 0 {
		return errors.New("invalid Runtime history source duration")
	}
	if record.SourceDuration > 0 && record.ResolvedAt.Add(-record.SourceDuration).Unix() < 0 {
		return errors.New("invalid Runtime history source start time")
	}
	const maxSafeInteger = uint64(1<<53 - 1)
	if record.TokenUsage != nil && (record.TokenUsage.InputTokens > maxSafeInteger || record.TokenUsage.OutputTokens > maxSafeInteger) {
		return errors.New("invalid Runtime history token usage")
	}
	if record.Sample == nil {
		return nil
	}
	return validateSample(record.Sample, record.ResolvedAt)
}

func validateSample(sample *runtimeobs.Sample, resolvedAt time.Time) error {
	if sample.ObservedAt.IsZero() || sample.ObservedAt.Unix() < 0 || sample.ObservedAt.After(resolvedAt) {
		return errors.New("invalid Runtime history sample time")
	}
	if sample.StartedAt != nil && (sample.StartedAt.IsZero() || sample.StartedAt.Unix() < 0 || sample.StartedAt.After(sample.ObservedAt)) {
		return errors.New("invalid Runtime history start time")
	}
	if sample.CPUUsageSecondsTotal != nil && (*sample.CPUUsageSecondsTotal < 0 || math.IsNaN(*sample.CPUUsageSecondsTotal) || math.IsInf(*sample.CPUUsageSecondsTotal, 0)) {
		return errors.New("invalid Runtime history CPU usage")
	}
	if sample.CPUCapacityCores != nil && (*sample.CPUCapacityCores <= 0 || math.IsNaN(*sample.CPUCapacityCores) || math.IsInf(*sample.CPUCapacityCores, 0)) {
		return errors.New("invalid Runtime history CPU capacity")
	}
	if sample.CPUUtilizationRatio != nil && (*sample.CPUUtilizationRatio < 0 || math.IsNaN(*sample.CPUUtilizationRatio) || math.IsInf(*sample.CPUUtilizationRatio, 0)) {
		return errors.New("invalid Runtime history CPU utilization")
	}
	const maxSafeInteger = uint64(1<<53 - 1)
	if sample.MemoryUsageBytes != nil && *sample.MemoryUsageBytes > maxSafeInteger {
		return errors.New("invalid Runtime history memory usage")
	}
	if sample.MemoryLimitBytes != nil && (*sample.MemoryLimitBytes == 0 || *sample.MemoryLimitBytes > maxSafeInteger) {
		return errors.New("invalid Runtime history memory limit")
	}
	return nil
}

func recordAttributes(record runtimeobs.ExportRecord) attribute.Set {
	values := []attribute.KeyValue{
		attribute.String("agents.tenant.id", record.TenantID),
		attribute.String("agents.session.id", record.SessionID),
		attribute.String("agents.runtime.mode", string(record.Mode)),
		attribute.String("agents.runtime.status", string(record.Status)),
		attribute.String("agents.runtime.collection.source", string(record.CollectionSource)),
		attribute.Int64("agents.runtime.resolved_at_unix_nano", record.ResolvedAt.UnixNano()),
	}
	if record.EnvironmentID != "" {
		values = append(values, attribute.String("agents.environment.id", record.EnvironmentID))
	}
	if record.AllocationID != "" {
		values = append(values, attribute.String("agents.runtime.allocation.id", record.AllocationID))
	}
	if record.ProviderType != "" {
		values = append(values, attribute.String("agents.runtime.provider.type", record.ProviderType))
	}
	if record.Reason != "" {
		values = append(values, attribute.String("agents.runtime.reason", record.Reason))
	}
	if record.Sample != nil && record.Sample.StartedAt != nil {
		values = append(values, attribute.Int64("agents.runtime.compute.started_at_unix_nano", record.Sample.StartedAt.UnixNano()))
	}
	if record.Sample != nil {
		values = append(values, attribute.Int64("agents.runtime.observed_at_unix_nano", record.Sample.ObservedAt.UnixNano()))
	}
	return attribute.NewSet(values...)
}

func deltaCountMetric(name, description string, observedAt time.Time, attributes attribute.Set) metricdata.Metrics {
	return metricdata.Metrics{Name: name, Description: description, Unit: "{result}", Data: metricdata.Sum[int64]{
		DataPoints:  []metricdata.DataPoint[int64]{{Attributes: attributes, StartTime: observedAt, Time: observedAt, Value: 1}},
		Temporality: metricdata.DeltaTemporality, IsMonotonic: true,
	}}
}

func durationMetric(record runtimeobs.ExportRecord, attributes attribute.Set) metricdata.Metrics {
	duration := record.SourceDuration.Seconds()
	bounds := []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10}
	buckets := make([]uint64, len(bounds)+1)
	index := len(bounds)
	for i, bound := range bounds {
		if duration <= bound {
			index = i
			break
		}
	}
	buckets[index] = 1
	start := record.ResolvedAt.Add(-record.SourceDuration)
	return metricdata.Metrics{Name: SampleDurationName, Description: "Provider Runtime sample duration", Unit: "s", Data: metricdata.Histogram[float64]{
		DataPoints: []metricdata.HistogramDataPoint[float64]{{
			Attributes: attributes, StartTime: start, Time: record.ResolvedAt, Count: 1,
			Bounds: bounds, BucketCounts: buckets, Min: metricdata.NewExtrema(duration), Max: metricdata.NewExtrema(duration), Sum: duration,
		}},
		Temporality: metricdata.DeltaTemporality,
	}}
}

func cumulativeMetric(name, description, unit string, value float64, startedAt *time.Time, observedAt time.Time, attributes attribute.Set) metricdata.Metrics {
	point := metricdata.DataPoint[float64]{Attributes: attributes, Time: observedAt, Value: value}
	if startedAt != nil {
		point.StartTime = *startedAt
	}
	return metricdata.Metrics{Name: name, Description: description, Unit: unit, Data: metricdata.Sum[float64]{
		DataPoints: []metricdata.DataPoint[float64]{point}, Temporality: metricdata.CumulativeTemporality, IsMonotonic: true,
	}}
}

func gaugeMetric(name, description, unit string, value float64, observedAt time.Time, attributes attribute.Set) metricdata.Metrics {
	return metricdata.Metrics{Name: name, Description: description, Unit: unit, Data: metricdata.Gauge[float64]{
		DataPoints: []metricdata.DataPoint[float64]{{Attributes: attributes, Time: observedAt, Value: value}},
	}}
}

func integerGaugeMetric(name, description, unit string, value int64, observedAt time.Time, attributes attribute.Set) metricdata.Metrics {
	return metricdata.Metrics{Name: name, Description: description, Unit: unit, Data: metricdata.Gauge[int64]{
		DataPoints: []metricdata.DataPoint[int64]{{Attributes: attributes, Time: observedAt, Value: value}},
	}}
}

var _ sdkmetric.Exporter = (*otlpmetrichttp.Exporter)(nil)
var _ runtimeobs.Exporter = (*Exporter)(nil)
