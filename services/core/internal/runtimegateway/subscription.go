package runtimegateway

import (
	"errors"
	"fmt"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

var ErrSubscriberOverflow = errors.New("execution subscriber buffer overflow")

// Subscription streams one operation's frames, which all name ref.
type Subscription struct {
	Events <-chan proto.Envelope
	ch     chan proto.Envelope
	ref    proto.AssignmentRef
	mu     sync.Mutex
	err    error
	closed bool
}

func (s *Subscription) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Subscription) closeLocked(err error) {
	if !s.closed {
		s.err, s.closed = err, true
		close(s.ch)
	}
}

// deliverLocked queues env. A frame of another assignment or a full buffer
// ends the subscription with an error.
func (s *Subscription) deliverLocked(env proto.Envelope) bool {
	if env.Assignment != s.ref {
		s.closeLocked(errAssignmentEcho)
		return false
	}
	select {
	case s.ch <- env:
		return true
	default:
		s.closeLocked(ErrSubscriberOverflow)
		return false
	}
}

// SubscribeDurable reports transport loss and overflow separately from native
// execution events. Callers must inspect Err after Events closes. ref is the
// assignment that starts the run.
func (s *Session) SubscribeDurable(runID string, ref proto.AssignmentRef) (*Subscription, error) {
	if runID == "" {
		return nil, fmt.Errorf("agentdaemon gateway: Subscribe requires non-empty runID")
	}
	ch := make(chan proto.Envelope, 256)
	sub := &Subscription{Events: ch, ch: ch, ref: ref}
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	if s.IsClosed() {
		return nil, ErrSessionClosed
	}
	if existing := s.subs[runID]; existing != nil {
		existing.mu.Lock()
		existing.closeLocked(ErrSessionClosed)
		existing.mu.Unlock()
	}
	s.subs[runID] = sub
	s.reg.AttachRun(runID, s)
	return sub, nil
}

func (s *Session) Unsubscribe(runID string) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	if sub := s.subs[runID]; sub != nil {
		sub.mu.Lock()
		sub.closeLocked(nil)
		sub.mu.Unlock()
		delete(s.subs, runID)
	}
	s.reg.DetachRun(runID)
}

func (s *Session) closeSubscription(sub *Subscription) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed {
		return
	}
	sub.closeLocked(ErrSessionClosed)
}

func (s *Session) dispatchToSubscriber(env proto.Envelope) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	sub := s.subs[env.ID]
	if sub == nil {
		return
	}
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.deliverLocked(env) && env.Type == proto.TypeDone {
		sub.closeLocked(nil)
	}
	if sub.closed {
		delete(s.subs, env.ID)
		s.reg.DetachRun(env.ID)
	}
}
