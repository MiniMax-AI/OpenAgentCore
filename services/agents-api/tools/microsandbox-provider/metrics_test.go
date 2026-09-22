//go:build linux

package main

import (
	"testing"
	"time"

	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

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
