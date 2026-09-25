package execution

import (
	"context"
	"encoding/json"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/observability"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type journal struct {
	store                 eventWriter
	tenant, session, turn string
	next                  int32
	batch                 []store.ExecutionEvent
	batchTimes            []time.Time
	bytes                 int
	pendingCount          int
	observeSubagents      bool
	toolRecorder          interface {
		Record(observability.ToolAttempt)
	}
	toolStarts map[string]time.Time
}

type eventWriter interface {
	AppendTurnEvents(context.Context, string, string, string, int32, []store.ExecutionEvent) error
}

func recordCancellation(ctx context.Context, journal *journal, reply cancellationResult, result *Result) error {
	if reply.err != nil {
		return nil
	}
	env, err := proto.NewEnvelope("cancel_receipt", journal.turn, reply.ack)
	if err != nil {
		return err
	}
	if reply.ack.Applied && reply.ack.Outcome != nil {
		raw, err := json.Marshal(reply.ack.Outcome)
		if err != nil {
			return err
		}
		if err := result.mergeDone(raw); err != nil {
			return err
		}
	}
	return journal.observe(ctx, env)
}

func (j *journal) observe(ctx context.Context, env proto.Envelope) error {
	if err := j.enqueue(env); err != nil {
		return err
	}
	if j.bytes > 768*1024 || len(j.batch) >= 64 {
		return j.flush(ctx)
	}
	return nil
}

func (j *journal) enqueue(env proto.Envelope) error {
	if (env.Type == proto.TypeSubagentIdentity || env.Type == proto.TypeSubagentLifecycle || env.Type == proto.TypeSubagentTurn || env.Type == proto.TypeSubagentItem || env.Type == proto.TypeSubagentCoordination) && !j.observeSubagents {
		return store.ErrInvalidInput
	}
	switch env.Type {
	case proto.TypeDelta, proto.TypeOutputMessage, proto.TypeThinking, proto.TypeToolCall, proto.TypeCommandOutput, proto.TypeUsage,
		proto.TypeError, proto.TypeDone, proto.TypePromptSteerAck, proto.TypeSubagentIdentity, proto.TypeSubagentLifecycle, proto.TypeSubagentTurn, proto.TypeSubagentItem, proto.TypeSubagentCoordination, "cancel_receipt":
	default:
		return nil
	}
	if len(env.Payload) > 512*1024 {
		return store.ErrEventLimit
	}
	j.batch = append(j.batch, store.ExecutionEvent{Kind: env.Type, Payload: env.Payload})
	j.batchTimes = append(j.batchTimes, time.Now().UTC())
	j.bytes += len(env.Payload)
	return nil
}

func (j *journal) observeToolAttempt(raw json.RawMessage, observedAt time.Time) {
	if j.toolRecorder == nil {
		return
	}
	var call proto.ToolCallPayload
	if json.Unmarshal(raw, &call) != nil || call.ID == "" {
		return
	}
	if call.Stage == "before" {
		if j.toolStarts == nil {
			j.toolStarts = make(map[string]time.Time)
		}
		if _, exists := j.toolStarts[call.ID]; !exists {
			j.toolStarts[call.ID] = observedAt
		}
		return
	}
	if call.Stage != "after" {
		return
	}
	finished := observedAt
	var started *time.Time
	if value, exists := j.toolStarts[call.ID]; exists {
		started = &value
		delete(j.toolStarts, call.ID)
	}
	category, outcome := "other", "unknown"
	var duration *int64
	if call.Observation != nil {
		switch call.Observation.Kind {
		case "command", "mcp", "function", "web_search", "file":
			category = call.Observation.Kind
		}
		switch call.Observation.Status {
		case "completed":
			outcome = "success"
		case "failed":
			outcome = "error"
		}
		if call.Observation.DurationMS != nil && *call.Observation.DurationMS >= 0 {
			value := *call.Observation.DurationMS
			duration = &value
		}
	}
	if duration == nil && started != nil {
		value := finished.Sub(*started).Milliseconds()
		duration = &value
	}
	j.toolRecorder.Record(observability.ToolAttempt{
		ID:       uuid.NewSHA1(uuid.NameSpaceOID, []byte(j.turn+":"+call.ID)).String(),
		TenantID: j.tenant, SessionID: j.session, TurnID: j.turn,
		StartedAt: started, FinishedAt: finished, Category: category, Outcome: outcome, DurationMS: duration,
	})
}

func (j *journal) flush(ctx context.Context) error {
	if len(j.batch) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for len(j.batch) > 0 {
		count, size := 0, 0
		limit := 64
		if j.pendingCount > 0 {
			limit = j.pendingCount
		}
		for count < len(j.batch) && count < limit {
			length := len(j.batch[count].Payload)
			if count > 0 && size+length > 768*1024 {
				break
			}
			size += length
			count++
		}
		// An uncertain commit must retry the same batch even after more frames arrive.
		j.pendingCount = count
		if err := j.store.AppendTurnEvents(ctx, j.tenant, j.session, j.turn, j.next, j.batch[:count]); err != nil {
			return err
		}
		for index, event := range j.batch[:count] {
			if event.Kind == proto.TypeToolCall {
				j.observeToolAttempt(event.Payload, j.batchTimes[index])
			}
		}
		j.pendingCount = 0
		j.next += int32(count)
		j.bytes -= size
		j.batch = j.batch[count:]
		j.batchTimes = j.batchTimes[count:]
	}
	j.batch = nil
	j.batchTimes = nil
	return nil
}

// Cancellation receipts use a separate waiter; preceding frames can still be queued.
func (j *journal) drain(upstream <-chan proto.Envelope, result *Result) error {
	var observedErr error
	for range 256 {
		select {
		case env, ok := <-upstream:
			if !ok {
				return observedErr
			}
			if err := j.enqueue(env); err != nil {
				observedErr = err
			}
			if err := result.mergeObservation(env); err != nil {
				observedErr = err
			}
		default:
			return observedErr
		}
	}
	return observedErr
}
