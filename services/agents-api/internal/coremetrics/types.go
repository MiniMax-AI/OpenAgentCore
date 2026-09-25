// Package coremetrics collects bounded operational measurements for administrators.
package coremetrics

import (
	"context"
	"time"
)

type Latency struct {
	P50 *float64 `json:"p50" extensions:"x-nullable"`
	P95 *float64 `json:"p95" extensions:"x-nullable"`
}
type Range struct {
	Start             time.Time `json:"start"`
	End               time.Time `json:"end"`
	ResolutionSeconds int64     `json:"resolution_seconds"`
}
type ServiceState struct {
	Status         string     `json:"status"`
	Revision       *string    `json:"revision" extensions:"x-nullable"`
	StartedAt      *time.Time `json:"started_at" extensions:"x-nullable"`
	ExecutionOwner *bool      `json:"execution_owner" extensions:"x-nullable"`
}
type ExecutionBucket struct {
	Start          time.Time `json:"start"`
	Queued         *int64    `json:"queued" extensions:"x-nullable"`
	InProgress     *int64    `json:"in_progress" extensions:"x-nullable"`
	QueueWaitP95MS *float64  `json:"queue_wait_p95_ms" extensions:"x-nullable"`
}
type DatabaseBucket struct {
	Start     time.Time `json:"start"`
	PingP95MS *float64  `json:"ping_p95_ms" extensions:"x-nullable"`
	PoolInUse *int64    `json:"pool_in_use" extensions:"x-nullable"`
}
type Pool struct {
	InUse *int64 `json:"in_use" extensions:"x-nullable"`
	Idle  *int64 `json:"idle" extensions:"x-nullable"`
	Max   *int64 `json:"max" extensions:"x-nullable"`
}
type Execution struct {
	SlotsInUse          *int64            `json:"slots_in_use" extensions:"x-nullable"`
	SlotsTotal          *int64            `json:"slots_total" extensions:"x-nullable"`
	QueuedTurns         *int64            `json:"queued_turns" extensions:"x-nullable"`
	WaitingForDaemon    *int64            `json:"waiting_for_daemon" extensions:"x-nullable"`
	InProgressTurns     *int64            `json:"in_progress_turns" extensions:"x-nullable"`
	OldestQueuedSeconds *float64          `json:"oldest_queued_seconds" extensions:"x-nullable"`
	ConnectedDaemons    *int64            `json:"connected_daemons" extensions:"x-nullable"`
	Interrupted         *int64            `json:"interrupted" extensions:"x-nullable"`
	Unavailable         *int64            `json:"unavailable" extensions:"x-nullable"`
	QueueWaitMS         Latency           `json:"queue_wait_ms"`
	Series              []ExecutionBucket `json:"series"`
}
type Database struct {
	PingMS    Latency          `json:"ping_ms"`
	Pool      Pool             `json:"pool"`
	SizeBytes *int64           `json:"size_bytes" extensions:"x-nullable"`
	Series    []DatabaseBucket `json:"series"`
}
type Job struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	LastRunAt *time.Time `json:"last_run_at" extensions:"x-nullable"`
	Processed *int64     `json:"processed" extensions:"x-nullable"`
	Failed    *int64     `json:"failed" extensions:"x-nullable"`
}
type Process struct {
	MemoryBytes *uint64 `json:"memory_bytes" extensions:"x-nullable"`
	Goroutines  *int64  `json:"goroutines" extensions:"x-nullable"`
}
type View struct {
	Object    string       `json:"object"`
	Range     Range        `json:"range"`
	Service   ServiceState `json:"service"`
	Execution Execution    `json:"execution"`
	Database  Database     `json:"database"`
	Jobs      []Job        `json:"jobs"`
	Process   Process      `json:"process"`
}

// Sample contains only measurements, never resource identities or error text.
type Sample struct {
	At                                   time.Time
	Queued, WaitingForDaemon, InProgress *int64
	OldestQueuedSeconds                  *float64
	PingMS                               *float64
	PoolInUse, DatabaseSize              *int64
	Maintenance                          *bool
	Healthy                              bool
}
type Live struct {
	SlotsInUse, SlotsTotal, ConnectedDaemons *int64
	ExecutionOwner                           *bool
	Pool                                     Pool
	Scheduler                                Job
}
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
