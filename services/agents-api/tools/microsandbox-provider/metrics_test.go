//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	wire "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/microsandbox"
)

func nativeMetrics(timestamp, uptime string, cpu uint64) []byte {
	return []byte(fmt.Sprintf(`{"name":"owned","state":"running","timestamp":%q,"uptime_secs":%s,"vcpu_time_ns":%d,"memory_bytes":4096,"memory_limit_bytes":8192,"cpu_percent":72.5}`, timestamp, uptime, cpu))
}

func TestProjectMetricsRetainsNativeStartAcrossPollsAndReplacement(t *testing.T) {
	var start time.Time
	for index, input := range []struct {
		timestamp, uptime string
		cpu               uint64
	}{
		{"2026-09-22T12:00:00.123Z", "300.001", 2_500_000_000},
		{"2026-09-22T12:00:01.999Z", "301.877", 3_500_000_000},
		{"2026-09-22T12:00:04.124Z", "0.001", 1_000_000},
	} {
		sample, err := projectMetrics(nativeMetrics(input.timestamp, input.uptime, input.cpu), "owned")
		if err != nil {
			t.Fatal(err)
		}
		if sample.VCPUTimeNs != input.cpu || sample.MemoryBytes != 4096 || sample.MemoryLimitBytes != 8192 {
			t.Fatalf("bad sample: %+v", sample)
		}
		actualStart := sample.ObservedAt.Add(-sample.Uptime)
		if index == 0 {
			start = actualStart
		}
		if (index < 2) != actualStart.Equal(start) {
			t.Fatalf("poll %d start=%s first=%s", index, actualStart, start)
		}
	}
}

func TestProjectMetricsRecoversNativeFloatingSerializationAtMillisecondPrecision(t *testing.T) {
	for _, uptime := range []string{"1.118", "1.1179999999999999", "1.1180000000000001"} {
		sample, err := projectMetrics(nativeMetrics("2026-09-22T12:00:00.123Z", uptime, 123), "owned")
		if err != nil || sample.Uptime != 1118*time.Millisecond {
			t.Fatalf("uptime %s: sample=%+v error=%v", uptime, sample, err)
		}
	}
}

func TestProjectMetricsRejectsUnqualifiedReports(t *testing.T) {
	valid := string(nativeMetrics("2026-09-22T12:00:00.123Z", "300.001", 123))
	for _, input := range []string{
		`{}`, valid + `{}`, strings.Replace(valid, `"owned"`, `"foreign"`, 1),
		strings.Replace(valid, `"timestamp":"2026-09-22T12:00:00.123Z"`, `"timestamp":"2026-09-22T12:00:00.123456Z"`, 1),
		strings.Replace(valid, `"vcpu_time_ns":123,`, ``, 1),
		strings.Replace(valid, `"memory_limit_bytes":8192`, `"memory_limit_bytes":0`, 1),
		strings.Replace(valid, `"uptime_secs":300.001,`, ``, 1),
	} {
		if _, err := projectMetrics([]byte(input), "owned"); !errors.Is(err, wire.ErrUnconfirmed) {
			t.Fatalf("accepted malformed report: %s, %v", input, err)
		}
	}
	for _, uptime := range []string{"-1", "0.0001", "1e100000000", "999999999999999999999999999", "99999999999", "null"} {
		if _, err := projectMetrics(nativeMetrics("2026-09-22T12:00:00.123Z", uptime, 123), "owned"); !errors.Is(err, wire.ErrUnconfirmed) {
			t.Fatalf("accepted uptime %s: %v", uptime, err)
		}
	}
	for _, state := range []string{"stalled", "exited", ""} {
		if _, err := projectMetrics([]byte(strings.Replace(valid, `"running"`, fmt.Sprintf("%q", state), 1)), "owned"); !errors.Is(err, runtimeobs.ErrUnavailable) {
			t.Fatalf("accepted state %q: %v", state, err)
		}
	}
}

func TestReadMetricsUsesBoundedReadOnlyNativeCommand(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "msb")
	body := "#!/bin/sh\n[ \"$*\" = 'metrics owned --format json' ] || exit 2\nprintf '%s' '" + string(nativeMetrics("2026-09-22T12:00:00.123Z", "300.001", 123)) + "'\n"
	if err := os.WriteFile(binary, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if sample, err := readMetrics(ctx, binary, "owned"); err != nil || sample.VCPUTimeNs != 123 {
		t.Fatalf("sample=%+v error=%v", sample, err)
	}
	if _, err := readMetrics(ctx, binary, "foreign"); !errors.Is(err, runtimeobs.ErrUnavailable) {
		t.Fatalf("command failure leaked: %v", err)
	}
	cancel()
	if _, err := readMetrics(ctx, binary, "owned"); !errors.Is(err, context.Canceled) {
		t.Fatalf("deadline ignored: %v", err)
	}
	output := &metricsOutput{}
	if _, err := output.Write(make([]byte, 64*1024+1)); err == nil || output.Len() != 0 {
		t.Fatal("unbounded metrics output")
	}
}
