//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	wire "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/microsandbox"
)

func (b backend) metrics(ctx context.Context, c wire.Compute) (*wire.Metrics, error) {
	ctx, cancel := context.WithDeadline(ctx, b.q.Deadline)
	defer cancel()
	_, state, err := b.inspect(ctx, c)
	if err != nil {
		return nil, err
	}
	if state.Status != "running" && state.Status != "draining" {
		return nil, sandbox.ErrNotFound
	}
	// The allocation lock and exact SDK identity checks fence the name-based
	// CLI read. The pinned CLI preserves the registry sample timestamp and
	// millisecond uptime that the Go SDK's Metrics projection discards.
	metrics, err := readMetrics(ctx, b.q.Config.RuntimePath, c.Name)
	if err != nil {
		return nil, err
	}
	_, current, err := b.inspect(ctx, state.Compute)
	if err != nil {
		return nil, err
	}
	if current.Status != "running" && current.Status != "draining" {
		return nil, sandbox.ErrNotFound
	}
	return metrics, nil
}

func readMetrics(ctx context.Context, runtimePath, name string) (*wire.Metrics, error) {
	command := exec.CommandContext(ctx, runtimePath, "metrics", name, "--format", "json")
	command.WaitDelay = time.Second
	output := &metricsOutput{}
	command.Stdout = output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, runtimeobs.ErrUnavailable
	}
	return projectMetrics(output.Bytes(), name)
}

type metricsOutput struct{ bytes.Buffer }

func (b *metricsOutput) Write(value []byte) (int, error) {
	if len(value) > 64*1024-b.Len() {
		return 0, io.ErrShortWrite
	}
	return b.Buffer.Write(value)
}

func projectMetrics(data []byte, name string) (*wire.Metrics, error) {
	var report struct {
		Name             string      `json:"name"`
		State            string      `json:"state"`
		Timestamp        time.Time   `json:"timestamp"`
		UptimeSeconds    json.Number `json:"uptime_secs"`
		VCPUTimeNs       *uint64     `json:"vcpu_time_ns"`
		MemoryBytes      *uint64     `json:"memory_bytes"`
		MemoryLimitBytes *uint64     `json:"memory_limit_bytes"`
	}
	if json.Unmarshal(data, &report) != nil || report.Name != name || report.Timestamp.IsZero() ||
		!report.Timestamp.Equal(report.Timestamp.Truncate(time.Millisecond)) ||
		report.VCPUTimeNs == nil || report.MemoryBytes == nil || report.MemoryLimitBytes == nil || *report.MemoryLimitBytes == 0 {
		return nil, wire.ErrUnconfirmed
	}
	if report.State != "running" {
		return nil, runtimeobs.ErrUnavailable
	}
	// Registry timestamps are integer milliseconds. Parse the CLI decimal
	// exactly, without float-to-nanosecond rounding changing the start fence.
	seconds, fraction, _ := strings.Cut(string(report.UptimeSeconds), ".")
	if seconds == "" || len(fraction) > 3 || strings.ContainsAny(seconds+fraction, "-+eE/") {
		return nil, wire.ErrUnconfirmed
	}
	milliseconds, err := strconv.ParseInt(seconds+fraction+strings.Repeat("0", 3-len(fraction)), 10, 64)
	if err != nil || milliseconds < 0 {
		return nil, wire.ErrUnconfirmed
	}
	if milliseconds > int64((1<<63-1)/time.Millisecond) || report.Timestamp.UnixMilli() < milliseconds {
		return nil, wire.ErrUnconfirmed
	}
	return &wire.Metrics{
		ObservedAt: report.Timestamp, Uptime: time.Duration(milliseconds) * time.Millisecond,
		VCPUTimeNs: *report.VCPUTimeNs, MemoryBytes: *report.MemoryBytes, MemoryLimitBytes: *report.MemoryLimitBytes,
	}, nil
}
