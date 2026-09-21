package codex

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

const subagentLookupTimeout = 3 * time.Second

type subagentObservations struct {
	cancelling   atomic.Bool
	cancelResult error
	interrupted  map[string]bool

	mu       sync.Mutex
	sealed   bool
	ctx      context.Context
	cancel   context.CancelFunc
	wake     chan struct{}
	terminal chan []proto.Envelope
	done     chan struct{}
	sent     map[string]string
}

func (s *Session) startSubagentObservations() {
	ctx, cancel := context.WithCancel(s.cancelCtx)
	s.subagents = &subagentObservations{ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), terminal: make(chan []proto.Envelope, 1), done: make(chan struct{}), sent: map[string]string{}, interrupted: map[string]bool{}}
	go s.collectSubagentFacts()
}

// Notifications only wake the history reader; they never establish ownership or
// infer successful lifecycle effects from the native tool's completion status.
func (s *Session) observeSubagentIdentity(raw json.RawMessage) {
	if s.subagents == nil {
		return
	}
	var event struct {
		ThreadID string                `json:"threadId"`
		Item     subagentCollaboration `json:"item"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return
	}
	if event.Item.Type != "collabAgentToolCall" && event.ThreadID == s.currentThreadID() {
		return
	}
	select {
	case s.subagents.wake <- struct{}{}:
	default:
	}
}

func (s *Session) collectSubagentFacts() {
	o := s.subagents
	defer close(o.done)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var terminal []proto.Envelope
	var firstFailure time.Time
	var started bool
	for {
		select {
		case <-o.ctx.Done():
			o.mu.Lock()
			o.cancelResult = o.ctx.Err()
			o.mu.Unlock()
			return
		case terminal = <-o.terminal:
			started = true
		case <-o.wake:
			started = true
		case <-ticker.C:
		}
		if !started {
			continue
		}
		if s.currentThreadID() == "" {
			if terminal != nil {
				s.publishSubagentTerminal(terminal, nil)
				return
			}
			continue
		}
		busy, err := s.snapshotSubagents(o.ctx)
		if err != nil {
			if firstFailure.IsZero() {
				firstFailure = time.Now()
			}
			if time.Since(firstFailure) < subagentLookupTimeout {
				continue
			}
			if terminal == nil {
				s.emitTerminal("codex: subagent facts could not be confirmed", true)
				select {
				case terminal = <-o.terminal:
				case <-o.ctx.Done():
					o.mu.Lock()
					o.cancelResult = o.ctx.Err()
					o.mu.Unlock()
					return
				}
			}
			o.mu.Lock()
			o.cancelResult = err
			o.mu.Unlock()
			s.publishSubagentTerminal(terminal, err)
			return
		}
		firstFailure = time.Time{}
		if terminal != nil && !busy {
			s.publishSubagentTerminal(terminal, nil)
			return
		}
	}
}

func (s *Session) publishSubagentTerminal(events []proto.Envelope, cause error) {
	if cause != nil {
		s.cfg.logger.Warn("codex: subagent observation failed", "run_id", s.runID, "reason", cause.Error())
		failure, _ := proto.NewEnvelope(proto.TypeError, s.runID, proto.ErrorPayload{Error: cause.Error()})
		s.trySend(failure)
	}
	for _, event := range events {
		s.trySend(event)
	}
	s.closeOut()
}

// Freeze the root outcome on the native reader, then leave that reader available
// until the existing Run owner has delivered all finite child work. Cancellation
// remains the owner's boundary; there is no timeout that silently drops children.
func (s *Session) sendTerminal(events ...proto.Envelope) {
	if s.subagents == nil {
		for _, event := range events {
			s.trySend(event)
		}
		return
	}
	o := s.subagents
	o.mu.Lock()
	o.sealed = true
	o.mu.Unlock()
	o.terminal <- events
}

func (s *Session) closeRunOutput() {
	if o := s.subagents; o != nil {
		o.mu.Lock()
		sealed := o.sealed
		o.mu.Unlock()
		if !sealed {
			o.cancel()
		}
		<-o.done
		o.cancel()
	}
	s.closeOut()
}

func (s *Session) sendSubagentFact(ctx context.Context, kind, key string, payload any) error {
	env, err := proto.NewEnvelope(kind, s.runID, payload)
	if err != nil {
		return err
	}
	value := string(env.Payload)
	key = kind + ":" + key
	if s.subagents.sent[key] == value {
		return nil
	}
	if !s.sendWithin(ctx, env) {
		return errors.New("codex: subagent observation delivery unavailable")
	}
	s.subagents.sent[key] = value
	return nil
}
