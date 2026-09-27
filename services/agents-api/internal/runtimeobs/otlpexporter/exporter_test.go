package otlpexporter

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	collectorv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
)

type captureClient struct {
	metrics *metricdata.ResourceMetrics
	calls   int
	closed  bool
}

func (c *captureClient) Export(_ context.Context, metrics *metricdata.ResourceMetrics) error {
	c.calls++
	c.metrics = metrics
	return nil
}

func (c *captureClient) Shutdown(context.Context) error {
	c.closed = true
	return nil
}

func observedRecord() runtimeobs.ExportRecord {
	observedAt := time.Date(2026, 9, 23, 3, 0, 0, 0, time.UTC)
	startedAt := observedAt.Add(-2 * time.Minute)
	cpuUsage, capacity := 45.5, 2.0
	memoryUsage, memoryLimit := uint64(1024), uint64(2048)
	return runtimeobs.ExportRecord{
		TenantID: "tenant", SessionID: "session", EnvironmentID: "environment", AllocationID: "allocation",
		Mode: runtimeobs.ModeManaged, ProviderType: "docker", Status: runtimeobs.StatusObserved,
		CollectionSource: runtimeobs.CollectionSourceOnRead,
		ResolvedAt:       observedAt, SourceDuration: 50 * time.Millisecond,
		Sample: &runtimeobs.Sample{
			ObservedAt: observedAt, StartedAt: &startedAt,
			CPUUsageSecondsTotal: &cpuUsage, CPUCapacityCores: &capacity,
			MemoryUsageBytes: &memoryUsage, MemoryLimitBytes: &memoryLimit,
		},
	}
}

func TestExporterBuildsFencedProviderNeutralMetrics(t *testing.T) {
	client := &captureClient{}
	exporter := newWithClient(client)
	if err := exporter.Export(t.Context(), observedRecord()); err != nil {
		t.Fatal(err)
	}
	if client.calls != 1 || client.metrics == nil || len(client.metrics.ScopeMetrics) != 1 {
		t.Fatalf("unexpected export call: calls=%d metrics=%+v", client.calls, client.metrics)
	}
	serviceName, ok := client.metrics.Resource.Set().Value("service.name")
	if !ok || serviceName.AsString() != "oac-core" {
		t.Fatalf("unexpected service resource: %v %v", serviceName, ok)
	}

	namespace, ok := client.metrics.Resource.Set().Value("service.namespace")
	if !ok || namespace.AsString() != "oac" {
		t.Fatalf("unexpected service namespace: %v %v", namespace, ok)
	}

	byName := map[string]metricdata.Metrics{}
	for _, metric := range client.metrics.ScopeMetrics[0].Metrics {
		byName[metric.Name] = metric
	}
	for _, name := range []string{SampleName, SampleDurationName, CPUUsageName, CPUCapacityName, MemoryUsageName, MemoryLimitName} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("missing metric %q in %#v", name, byName)
		}
	}
	cpu, ok := byName[CPUUsageName].Data.(metricdata.Sum[float64])
	if !ok || cpu.Temporality != metricdata.CumulativeTemporality || !cpu.IsMonotonic || len(cpu.DataPoints) != 1 || cpu.DataPoints[0].Value != 45.5 {
		t.Fatalf("unexpected cumulative CPU metric: %#v", byName[CPUUsageName].Data)
	}
	attrs := attributeStrings(cpu.DataPoints[0].Attributes.ToSlice())
	for key, want := range map[string]string{
		"agents.tenant.id": "tenant", "agents.session.id": "session", "agents.environment.id": "environment",
		"agents.runtime.allocation.id": "allocation", "agents.runtime.mode": "openai_hosted",
		"agents.runtime.provider.type": "docker", "agents.runtime.status": "observed",
		"agents.runtime.collection.source":     "on_read",
		"agents.runtime.resolved_at_unix_nano": "1790132400000000000",
		"agents.runtime.observed_at_unix_nano": "1790132400000000000",
	} {
		if attrs[key] != want {
			t.Fatalf("attribute %q = %q, want %q", key, attrs[key], want)
		}
	}
	if _, ok := attrs["agents.runtime.compute.started_at_unix_nano"]; !ok {
		t.Fatal("compute incarnation fence is missing")
	}
	if err := exporter.Close(t.Context()); err != nil || !client.closed {
		t.Fatalf("exporter did not close: %v closed=%v", err, client.closed)
	}
}

func TestExporterPreservesUnavailableWithoutInventingResourceValues(t *testing.T) {
	client := &captureClient{}
	exporter := newWithClient(client)
	record := runtimeobs.ExportRecord{
		TenantID: "tenant", SessionID: "session", EnvironmentID: "environment", AllocationID: "allocation",
		Mode: runtimeobs.ModeManaged, Status: runtimeobs.StatusUnavailable, Reason: "sample_timeout",
		CollectionSource: runtimeobs.CollectionSourcePeriodic,
		ResolvedAt:       time.Date(2026, 9, 23, 3, 0, 0, 0, time.UTC),
	}
	if err := exporter.Export(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	metrics := client.metrics.ScopeMetrics[0].Metrics
	if len(metrics) != 1 || metrics[0].Name != SampleName {
		t.Fatalf("unavailable result invented measurements: %#v", metrics)
	}
}

func TestExporterEmitsCanonicalSessionTokenGaugesWithoutProviderValues(t *testing.T) {
	client := &captureClient{}
	exporter := newWithClient(client)
	record := runtimeobs.ExportRecord{
		TenantID: "tenant", SessionID: "session", EnvironmentID: "environment",
		Mode: runtimeobs.ModeManaged, Status: runtimeobs.StatusUnavailable, Reason: "sample_timeout",
		CollectionSource: runtimeobs.CollectionSourcePeriodic,
		ResolvedAt:       time.Date(2026, 9, 23, 3, 0, 0, 0, time.UTC),
		TokenUsage:       &runtimeobs.TokenUsage{InputTokens: 120, OutputTokens: 30},
	}
	if err := exporter.Export(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	metrics := client.metrics.ScopeMetrics[0].Metrics
	if len(metrics) != 3 || metrics[0].Name != SampleName || metrics[1].Name != TokenInputName || metrics[2].Name != TokenOutputName {
		t.Fatalf("unexpected token metrics: %#v", metrics)
	}
	input, ok := metrics[1].Data.(metricdata.Gauge[int64])
	if !ok || len(input.DataPoints) != 1 || input.DataPoints[0].Value != 120 {
		t.Fatalf("unexpected input token gauge: %#v", metrics[1].Data)
	}
}

func TestExporterPreservesObservedCoverageWithoutUnfencedResourceValues(t *testing.T) {
	client := &captureClient{}
	exporter := newWithClient(client)
	record := observedRecord()
	record.Sample.StartedAt = nil

	if err := exporter.Export(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	metrics := client.metrics.ScopeMetrics[0].Metrics
	if len(metrics) != 2 || metrics[0].Name != SampleName || metrics[1].Name != SampleDurationName {
		t.Fatalf("unfenced sample exported resource measurements: %#v", metrics)
	}
	for _, metric := range metrics {
		attributes := metricAttributes(metric)
		if _, ok := attributes.Value("agents.runtime.compute.started_at_unix_nano"); ok {
			t.Fatalf("unfenced sample invented a compute incarnation attribute: %#v", metric)
		}
	}
}

func TestExporterRejectsUnsafeRecordsBeforeTransport(t *testing.T) {
	client := &captureClient{}
	exporter := newWithClient(client)
	record := observedRecord()
	record.ProviderType = "docker native=secret"
	if err := exporter.Export(t.Context(), record); err == nil || client.calls != 0 {
		t.Fatalf("unsafe record reached transport: err=%v calls=%d", err, client.calls)
	}
	record = observedRecord()
	record.Sample = nil
	if err := exporter.Export(t.Context(), record); err == nil || client.calls != 0 {
		t.Fatalf("invalid observed record reached transport: err=%v calls=%d", err, client.calls)
	}
	record = observedRecord()
	record.SourceDuration = time.Duration(math.MaxInt64)
	if err := exporter.Export(t.Context(), record); err == nil || client.calls != 0 {
		t.Fatalf("invalid source start reached transport: err=%v calls=%d", err, client.calls)
	}
}

func TestOTLPHTTPTransportSendsProtobufAndServerOnlyHeaders(t *testing.T) {
	requests := make(chan *collectorv1.ExportMetricsServiceRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/metrics" {
			t.Errorf("unexpected OTLP path %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-only" {
			t.Error("configured authorization header was not sent")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var decoded collectorv1.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &decoded); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- &decoded
		response, _ := proto.Marshal(&collectorv1.ExportMetricsServiceResponse{})
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(response)
	}))
	defer server.Close()

	exporter, err := New(t.Context(), Config{
		Endpoint: server.URL + "/v1/metrics", Insecure: true,
		Headers: map[string]string{"Authorization": "Bearer test-only"}, RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = exporter.Close(t.Context()) }()
	if err := exporter.Export(t.Context(), observedRecord()); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-requests:
		if len(request.ResourceMetrics) != 1 || len(request.ResourceMetrics[0].ScopeMetrics) != 1 {
			t.Fatalf("unexpected OTLP request: %+v", request)
		}
		if got := len(request.ResourceMetrics[0].ScopeMetrics[0].Metrics); got != 6 {
			t.Fatalf("expected 6 OTLP metrics, got %d", got)
		}
	case <-time.After(time.Second):
		t.Fatal("OTLP request was not received")
	}
}

func attributeStrings(values []attribute.KeyValue) map[string]string {
	result := make(map[string]string, len(values))
	for _, value := range values {
		result[string(value.Key)] = value.Value.Emit()
	}
	return result
}

func metricAttributes(metric metricdata.Metrics) attribute.Set {
	switch data := metric.Data.(type) {
	case metricdata.Sum[int64]:
		return data.DataPoints[0].Attributes
	case metricdata.Histogram[float64]:
		return data.DataPoints[0].Attributes
	default:
		return attribute.NewSet()
	}
}
