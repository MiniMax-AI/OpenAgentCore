package observability

import (
	"context"
	"sync/atomic"
	"time"
)

// ToolAttempt is a sanitized terminal execution observation.
type ToolAttempt struct {
	ID         string
	TenantID   string
	SessionID  string
	TurnID     string
	StartedAt  *time.Time
	FinishedAt time.Time
	Category   string
	Outcome    string
	DurationMS *int64
}

type ToolSink interface {
	WriteToolAttempts(context.Context, []ToolAttempt, int64, int64) error
}

type ToolRecorder struct {
	queue   chan ToolAttempt
	sink    ToolSink
	dropped atomic.Int64
	failed  atomic.Int64
	done    chan struct{}
}

func NewToolRecorder(ctx context.Context, sink ToolSink) *ToolRecorder {
	r := &ToolRecorder{queue: make(chan ToolAttempt, 1024), sink: sink, done: make(chan struct{})}
	go r.run(ctx)
	return r
}

func (r *ToolRecorder) Record(value ToolAttempt) {
	select {
	case r.queue <- value:
	default:
		r.dropped.Add(1)
	}
}

func (r *ToolRecorder) Done() <-chan struct{} { return r.done }

func (r *ToolRecorder) run(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	pending := make([]ToolAttempt, 0, 128)
	flush := func() {
		dropped, failed := r.dropped.Swap(0), r.failed.Swap(0)
		if len(pending) == 0 && dropped == 0 && failed == 0 {
			return
		}
		writeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := r.sink.WriteToolAttempts(writeCtx, pending, dropped, failed)
		cancel()
		if err != nil {
			r.failed.Add(1)
			r.dropped.Add(dropped + int64(len(pending)))
		}
		pending = pending[:0]
	}
	for {
		select {
		case value := <-r.queue:
			pending = append(pending, value)
			if len(pending) >= 128 {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			for {
				select {
				case value := <-r.queue:
					pending = append(pending, value)
				default:
					flush()
					return
				}
			}
		}
	}
}
