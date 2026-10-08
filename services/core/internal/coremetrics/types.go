// Package coremetrics collects bounded operational measurements for administrators.
package coremetrics

import (
	"context"
	"errors"
	"time"
)

// ErrInvalidRange reports a Core metrics range that is not one of the supported
// windows, or a history interval that is unbounded or not aligned to its
// resolution.
var ErrInvalidRange = errors.New("invalid Core metrics range")

type JobStatus string

const (
	JobOk      JobStatus = "ok"
	JobFailing JobStatus = "failing"
	JobStopped JobStatus = "stopped"
	JobUnknown JobStatus = "unknown"
)

type ServiceStatus string

const (
	ServiceRunning  ServiceStatus = "running"
	ServiceDegraded ServiceStatus = "degraded"
)

type Latency struct {
	P50 *float64 `json:"p50" extensions:"x-nullable" binding:"required"`
	P95 *float64 `json:"p95" extensions:"x-nullable" binding:"required"`
}
type Range struct {
	Start             time.Time `json:"start" binding:"required"`
	End               time.Time `json:"end" binding:"required"`
	ResolutionSeconds int64     `json:"resolution_seconds" binding:"required"`
}
type ServiceState struct {
	Status         ServiceStatus `json:"status" binding:"required"`
	Revision       *string       `json:"revision" extensions:"x-nullable" binding:"required"`
	StartedAt      *time.Time    `json:"started_at" extensions:"x-nullable" binding:"required"`
	ExecutionOwner *bool         `json:"execution_owner" extensions:"x-nullable" binding:"required"`
}
type ExecutionBucket struct {
	Start          time.Time `json:"start" binding:"required"`
	Queued         *int64    `json:"queued" extensions:"x-nullable" binding:"required"`
	InProgress     *int64    `json:"in_progress" extensions:"x-nullable" binding:"required"`
	QueueWaitP95MS *float64  `json:"queue_wait_p95_ms" extensions:"x-nullable" binding:"required"`
}
type DatabaseBucket struct {
	Start     time.Time `json:"start" binding:"required"`
	PingP95MS *float64  `json:"ping_p95_ms" extensions:"x-nullable" binding:"required"`
	PoolInUse *int64    `json:"pool_in_use" extensions:"x-nullable" binding:"required"`
}
type Pool struct {
	InUse *int64 `json:"in_use" extensions:"x-nullable" binding:"required"`
	Idle  *int64 `json:"idle" extensions:"x-nullable" binding:"required"`
	Max   *int64 `json:"max" extensions:"x-nullable" binding:"required"`
}
type Execution struct {
	SlotsInUse          int64             `json:"slots_in_use" binding:"required"`
	SlotsTotal          int64             `json:"slots_total" binding:"required"`
	QueuedTurns         *int64            `json:"queued_turns" extensions:"x-nullable" binding:"required"`
	WaitingForDaemon    *int64            `json:"waiting_for_daemon" extensions:"x-nullable" binding:"required"`
	InProgressTurns     *int64            `json:"in_progress_turns" extensions:"x-nullable" binding:"required"`
	OldestQueuedSeconds *float64          `json:"oldest_queued_seconds" extensions:"x-nullable" binding:"required"`
	ConnectedDaemons    int64             `json:"connected_daemons" binding:"required"`
	Interrupted         *int64            `json:"interrupted" extensions:"x-nullable" binding:"required"`
	Unavailable         *int64            `json:"unavailable" extensions:"x-nullable" binding:"required"`
	QueueWaitMS         Latency           `json:"queue_wait_ms" binding:"required"`
	Series              []ExecutionBucket `json:"series" binding:"required"`
}
type Database struct {
	PingMS    Latency          `json:"ping_ms" binding:"required"`
	Pool      Pool             `json:"pool" binding:"required"`
	SizeBytes *int64           `json:"size_bytes" extensions:"x-nullable" binding:"required"`
	Series    []DatabaseBucket `json:"series" binding:"required"`
}
type Job struct {
	ID        string     `json:"id" binding:"required"`
	Status    JobStatus  `json:"status" binding:"required"`
	LastRunAt *time.Time `json:"last_run_at" extensions:"x-nullable" binding:"required"`
	Processed *int64     `json:"processed" extensions:"x-nullable" binding:"required"`
	Failed    *int64     `json:"failed" extensions:"x-nullable" binding:"required"`
}
type Process struct {
	MemoryBytes      *uint64         `json:"memory_bytes" extensions:"x-nullable" binding:"required"`
	Goroutines       *int64          `json:"goroutines" extensions:"x-nullable" binding:"required"`
	CPUCores         *float64        `json:"cpu_cores" extensions:"x-nullable" binding:"required"`
	CPULimitCores    *float64        `json:"cpu_limit_cores" extensions:"x-nullable" binding:"required"`
	RSSBytes         *uint64         `json:"rss_bytes" extensions:"x-nullable" binding:"required"`
	MemoryLimitBytes *uint64         `json:"memory_limit_bytes" extensions:"x-nullable" binding:"required"`
	Series           []ProcessBucket `json:"series" binding:"required"`
}
type ProcessBucket struct {
	Start    time.Time `json:"start" binding:"required"`
	CPUCores *float64  `json:"cpu_cores" extensions:"x-nullable" binding:"required"`
	RSSBytes *uint64   `json:"rss_bytes" extensions:"x-nullable" binding:"required"`
}
type View struct {
	Object    string       `json:"object" enums:"core.metrics" binding:"required"`
	Range     Range        `json:"range" binding:"required"`
	Service   ServiceState `json:"service" binding:"required"`
	Execution Execution    `json:"execution" binding:"required"`
	Database  Database     `json:"database" binding:"required"`
	Jobs      []Job        `json:"jobs" binding:"required"`
	Process   Process      `json:"process" binding:"required"`
}

// Sample contains only measurements, never resource identities or error text.
type Sample struct {
	At                                   time.Time
	Queued, WaitingForDaemon, InProgress *int64
	OldestQueuedSeconds                  *float64
	PingMS                               *float64
	PoolInUse, DatabaseSize              *int64
	Healthy                              bool
	Process                              Process
}
type Live struct {
	SlotsInUse, SlotsTotal, ConnectedDaemons int64
	ExecutionOwner                           *bool
	Pool                                     Pool
	Scheduler                                Job
}

// ExecutionSnapshot counts the deployment's persisted root Turns.
type ExecutionSnapshot struct {
	QueuedTurns, WaitingForDaemon, InProgressTurns int64
	OldestQueuedSeconds                            *float64
}

// History is the root Turn history of a range. Buckets maps each bucket's UTC
// start to its queue wait p95 in milliseconds, nil without observations.
type History struct {
	Interrupted int64
	QueueWaitMS Latency
	Buckets     map[time.Time]*float64
}
type Source interface {
	Sample(context.Context) Sample
	History(context.Context, time.Time, time.Time, time.Duration) (History, error)
	Live() Live
}
