package store

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

// Host observations are deployment telemetry, not sandbox capacity authority.
type RuntimeNodeHost struct {
	EffectiveCPUCores    *float64   `json:"effective_cpu_cores" extensions:"x-nullable"`
	CPUUtilization       *float64   `json:"cpu_utilization" extensions:"x-nullable"`
	TotalMemoryBytes     *int64     `json:"total_memory_bytes" extensions:"x-nullable"`
	AvailableMemoryBytes *int64     `json:"available_memory_bytes" extensions:"x-nullable"`
	AvailableDiskBytes   *int64     `json:"available_disk_bytes" extensions:"x-nullable"`
	ObservedAt           *time.Time `json:"observed_at" extensions:"x-nullable"`
}
type runtimeNodeHealthRecord struct {
	RuntimeNodeHealth
	Host *RuntimeNodeHost `json:"host,omitempty"`
}
type NodeHostHistoryPoint struct {
	Start                 time.Time `json:"start"`
	CPUUtilizationMax     *float64  `json:"cpu_utilization_max" extensions:"x-nullable"`
	MemoryUsedBytesMax    *int64    `json:"memory_used_bytes_max" extensions:"x-nullable"`
	AvailableDiskBytesMin *int64    `json:"available_disk_bytes_min" extensions:"x-nullable"`
}
type NodeHostHistory struct {
	ResolutionSeconds int64                  `json:"resolution_seconds"`
	Points            []NodeHostHistoryPoint `json:"points"`
}
type RuntimeNodeDetail struct {
	RuntimeNode
	Host    RuntimeNodeHost `json:"host"`
	History NodeHostHistory `json:"history"`
}

func validateRuntimeNodeHost(host *RuntimeNodeHost) error {
	if host == nil {
		return nil
	}
	if host.ObservedAt == nil || host.ObservedAt.IsZero() || host.ObservedAt.Year() < 1970 || host.ObservedAt.Year() > 9999 {
		return ErrInvalidInput
	}
	for _, value := range []*float64{host.CPUUtilization, host.EffectiveCPUCores} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
			return ErrInvalidInput
		}
	}
	if host.CPUUtilization != nil && *host.CPUUtilization > 1 || host.EffectiveCPUCores != nil && *host.EffectiveCPUCores == 0 {
		return ErrInvalidInput
	}
	for _, value := range []*int64{host.TotalMemoryBytes, host.AvailableMemoryBytes, host.AvailableDiskBytes} {
		if value != nil && (*value < 0 || *value > 1<<53-1) {
			return ErrInvalidInput
		}
	}
	if host.TotalMemoryBytes != nil && (*host.TotalMemoryBytes == 0 || host.AvailableMemoryBytes != nil && *host.AvailableMemoryBytes > *host.TotalMemoryBytes) {
		return ErrInvalidInput
	}
	return nil
}

// SampleNodeHostHistory runs with the existing Runtime sampler cadence. Only a
// fresh authenticated heartbeat is copied; neither reads nor offline nodes fill gaps.
func (s *Store) SampleNodeHostHistory(ctx context.Context) (int64, error) {
	return s.queries.SampleNodeHostHistory(ctx)
}

func (s *Store) GetRuntimeNodeDetail(ctx context.Context, id, name string) (RuntimeNodeDetail, error) {
	nodeID, err := parseConnectionGeneration(id)
	if err != nil {
		return RuntimeNodeDetail{}, ErrInvalidInput
	}
	if name != "1h" && name != "6h" && name != "24h" {
		return RuntimeNodeDetail{}, ErrInvalidInput
	}
	window, _ := coremetrics.Window(time.Now(), name)
	rows, err := s.queries.ListRuntimeNodes(ctx, nodeID)
	if err != nil {
		return RuntimeNodeDetail{}, err
	}
	if len(rows) == 0 {
		return RuntimeNodeDetail{}, ErrNotFound
	}
	nodes, err := runtimeNodeViews(rows)
	if err != nil {
		return RuntimeNodeDetail{}, err
	}
	var health runtimeNodeHealthRecord
	if err := json.Unmarshal(rows[0].Health, &health); err != nil {
		return RuntimeNodeDetail{}, err
	}
	value := RuntimeNodeDetail{RuntimeNode: nodes[0], History: NodeHostHistory{ResolutionSeconds: window.ResolutionSeconds, Points: make([]NodeHostHistoryPoint, 0)}}
	if health.Host != nil {
		value.Host = *health.Host
	}
	samples, err := s.queries.ListNodeHostHistory(ctx, sqlc.ListNodeHostHistoryParams{
		NodeID: nodeID, StartAt: pgtype.Timestamptz{Time: window.Start, Valid: true}, EndAt: pgtype.Timestamptz{Time: window.End, Valid: true},
		BucketWidth: pgtype.Interval{Microseconds: window.ResolutionSeconds * 1_000_000, Valid: true},
	})
	if err != nil {
		return RuntimeNodeDetail{}, err
	}
	byStart := make(map[time.Time]NodeHostHistoryPoint, len(samples))
	for _, sample := range samples {
		point := NodeHostHistoryPoint{Start: sample.Start.Time.UTC()}
		if sample.CpuSamples > 0 {
			point.CPUUtilizationMax = &sample.CpuUtilizationMax
		}
		if sample.MemorySamples > 0 {
			point.MemoryUsedBytesMax = &sample.MemoryUsedBytesMax
		}
		if sample.DiskSamples > 0 {
			point.AvailableDiskBytesMin = &sample.AvailableDiskBytesMin
		}
		byStart[point.Start] = point
	}
	for start := window.Start; start.Before(window.End); start = start.Add(time.Duration(window.ResolutionSeconds) * time.Second) {
		point, ok := byStart[start]
		if !ok {
			point.Start = start
		}
		value.History.Points = append(value.History.Points, point)
	}
	return value, nil
}
