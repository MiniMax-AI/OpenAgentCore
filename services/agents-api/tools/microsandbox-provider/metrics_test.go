//go:build linux

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

type fakeLiveMetrics struct {
	metrics       *sdk.Metrics
	metricsErr    error
	detachErr     error
	metricsCalled bool
	detachCalled  bool
	detachBounded bool
}

func (f *fakeLiveMetrics) Metrics(context.Context) (*sdk.Metrics, error) {
	f.metricsCalled = true
	return f.metrics, f.metricsErr
}

func (f *fakeLiveMetrics) Detach(ctx context.Context) error {
	f.detachCalled = true
	_, f.detachBounded = ctx.Deadline()
	return f.detachErr
}

func TestObserveConnectedMetricsSamplesLiveInstanceAndDetaches(t *testing.T) {
	want := &sdk.Metrics{VCPUTimeNs: 123}
	live := &fakeLiveMetrics{metrics: want}
	connected := false
	got, err := observeConnectedMetrics(context.Background(), func(context.Context) (liveMetricsSource, error) {
		connected = true
		return live, nil
	})
	if err != nil || got != want || !connected || !live.metricsCalled || !live.detachCalled || !live.detachBounded {
		t.Fatalf("unexpected observation: metrics=%+v err=%v connected=%t live=%+v", got, err, connected, live)
	}
}

func TestObserveConnectedMetricsPropagatesDetachFailure(t *testing.T) {
	want := errors.New("detach failed")
	live := &fakeLiveMetrics{metrics: &sdk.Metrics{VCPUTimeNs: 123}, detachErr: want}
	got, err := observeConnectedMetrics(context.Background(), func(context.Context) (liveMetricsSource, error) {
		return live, nil
	})
	if got != nil || !errors.Is(err, want) || !live.detachCalled || !live.detachBounded {
		t.Fatalf("detach failure not propagated: metrics=%+v err=%v live=%+v", got, err, live)
	}
}

func TestProjectMetricsKeepsOnlyCumulativeCommonFields(t *testing.T) {
	observed := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	projected := projectMetrics(&sdk.Metrics{
		CPUPercent: 72.5, VCPUTimeNs: 2_500_000_000,
		MemoryBytes: 4096, MemoryLimitBytes: 8192,
		DiskReadBytes: 100, DiskWriteBytes: 200, NetRxBytes: 300, NetTxBytes: 400,
		Uptime: 5 * time.Minute,
	}, observed)
	if projected.ObservedAt != observed || projected.Uptime != 5*time.Minute || projected.VCPUTimeNs != 2_500_000_000 || projected.MemoryBytes != 4096 || projected.MemoryLimitBytes != 8192 {
		t.Fatalf("bad projection: %+v", projected)
	}
}

func TestProjectMetricsRejectsMissingSDKSample(t *testing.T) {
	if projected := projectMetrics(nil, time.Now()); projected != nil {
		t.Fatalf("missing SDK sample projected: %+v", projected)
	}
}
