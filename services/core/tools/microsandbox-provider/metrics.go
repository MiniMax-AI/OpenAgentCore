//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/big"
	"os/exec"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	wire "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/microsandbox"
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
	decimal := string(report.UptimeSeconds)
	if len(decimal) == 0 || len(decimal) > 32 || strings.ContainsAny(decimal, "-+eE/") {
		return nil, wire.ErrUnconfirmed
	}
	uptime, ok := new(big.Rat).SetString(decimal)
	if !ok {
		return nil, wire.ErrUnconfirmed
	}
	uptime.Mul(uptime, big.NewRat(1000, 1))
	// as_secs_f64 can serialize 1.118 seconds as 1.1179999999999999.
	// Recover the nearest native millisecond exactly. Within Go's duration
	// range, floating serialization error stays below one microsecond.
	rounded := new(big.Rat).Add(uptime, big.NewRat(1, 2))
	millisecondsValue := new(big.Int).Quo(rounded.Num(), rounded.Denom())
	if !millisecondsValue.IsInt64() {
		return nil, wire.ErrUnconfirmed
	}
	deviation := new(big.Rat).Sub(uptime, new(big.Rat).SetInt(millisecondsValue))
	if deviation.Abs(deviation).Cmp(big.NewRat(1, 1000)) > 0 {
		return nil, wire.ErrUnconfirmed
	}
	milliseconds := millisecondsValue.Int64()
	if milliseconds > int64((1<<63-1)/time.Millisecond) || report.Timestamp.UnixMilli() < milliseconds {
		return nil, wire.ErrUnconfirmed
	}
	return &wire.Metrics{
		ObservedAt: report.Timestamp, Uptime: time.Duration(milliseconds) * time.Millisecond,
		VCPUTimeNs: *report.VCPUTimeNs, MemoryBytes: *report.MemoryBytes, MemoryLimitBytes: *report.MemoryLimitBytes,
	}, nil
}
