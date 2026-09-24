// Package coremetrics collects bounded operational measurements for administrators.
package coremetrics

import (
	"context"
	"time"
)

type Latency struct {
	P50 *float64 `json:"p50"`
	P95 *float64 `json:"p95"`
}
type Range struct {
	Start             time.Time `json:"start"`
	End               time.Time `json:"end"`
	ResolutionSeconds int64     `json:"resolution_seconds"`
}
type ServiceState struct {
	Status         string     `json:"status"`
	Revision       *string    `json:"revision"`
	StartedAt      *time.Time `json:"started_at"`
	ExecutionOwner *bool      `json:"execution_owner"`
}
type ExecutionBucket struct {
	Start          time.Time `json:"start"`
	Queued         *int64    `json:"queued"`
	InProgress     *int64    `json:"in_progress"`
	QueueWaitP95MS *float64  `json:"queue_wait_p95_ms"`
}
type DatabaseBucket struct {
	Start     time.Time `json:"start"`
	PingP95MS *float64  `json:"ping_p95_ms"`
	PoolInUse *int64    `json:"pool_in_use"`
}
type Pool struct {
	InUse *int64 `json:"in_use"`
	Idle  *int64 `json:"idle"`
	Max   *int64 `json:"max"`
}
type Execution struct {
	SlotsInUse          *int64            `json:"slots_in_use"`
	SlotsTotal          *int64            `json:"slots_total"`
	QueuedTurns         *int64            `json:"queued_turns"`
	WaitingForDaemon    *int64            `json:"waiting_for_daemon"`
	InProgressTurns     *int64            `json:"in_progress_turns"`
	OldestQueuedSeconds *float64          `json:"oldest_queued_seconds"`
	ConnectedDaemons    *int64            `json:"connected_daemons"`
	Interrupted         *int64            `json:"interrupted"`
	Unavailable         *int64            `json:"unavailable"`
	QueueWaitMS         Latency           `json:"queue_wait_ms"`
	Series              []ExecutionBucket `json:"series"`
}
type Database struct {
	PingMS    Latency          `json:"ping_ms"`
	Pool      Pool             `json:"pool"`
	SizeBytes *int64           `json:"size_bytes"`
	Series    []DatabaseBucket `json:"series"`
}
type Job struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	LastRunAt *time.Time `json:"last_run_at"`
	Processed *int64     `json:"processed"`
	Failed    *int64     `json:"failed"`
}
type Process struct {
	MemoryBytes *uint64 `json:"memory_bytes"`
	Goroutines  *int64  `json:"goroutines"`
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
