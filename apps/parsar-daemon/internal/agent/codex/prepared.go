package codex

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Prepared owns a connected native resource until Start transfers it to a Session.
// It observes owner cancellation and RPC exit, not continuous executor readiness.
type Prepared struct {
	mu                           sync.Mutex
	session                      *Session
	plan                         SessionPlan
	resumeID                     string
	strictResume                 bool
	requireExistingNativeSession bool
	claimed                      bool
	closed                       bool
	started                      bool
	transferred                  chan struct{}
}

var _ agent.PreparedCancellation = (*Prepared)(nil)

// Start consumes the preparation once. ctx bounds only this start operation;
// cancellation after return does not cancel the transferred Session. The original
// owner context remains its lifetime context. On success the Session owns out.
func (p *Prepared) Start(ctx context.Context, runID string, prompt proto.MessageInput, out chan<- proto.Envelope) (agent.Session, error) {
	session, err := p.start(ctx, runID, prompt, out)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (p *Prepared) start(ctx context.Context, runID string, prompt proto.MessageInput, out chan<- proto.Envelope) (*Session, error) {
	if out == nil || strings.TrimSpace(runID) == "" || prompt.Validate() != nil {
		return nil, errors.New("codex: start requires a run identity, prompt and output channel")
	}
	p.mu.Lock()
	if p.claimed || p.closed {
		p.mu.Unlock()
		return nil, errors.New("codex: preparation is no longer available")
	}
	p.claimed = true
	p.mu.Unlock()

	transferred := false
	defer func() {
		if !transferred {
			_ = p.Close()
		}
	}()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || ctx.Err() != nil || p.session.cancelCtx.Err() != nil || !p.session.rpc.Alive() {
		return nil, errors.New("codex: prepared harness is no longer available")
	}
	s := p.session
	s.runID, s.out = runID, out
	if s.observeSubagentIdentities {
		s.startSubagentObservations()
	}
	s.registerHandlers()
	p.started = true
	close(p.transferred)
	transferred = true
	req := proto.PromptRequestPayload{RunID: runID, Input: prompt, AgentSessionID: p.resumeID, StrictResume: p.strictResume, RequireExistingNativeSession: p.requireExistingNativeSession}
	go s.run(p.plan, req)
	return s, nil
}

// Close waits for unused teardown and plan cleanup, including another caller's
// ongoing Close. After successful Start it is inert; use
// the returned Session's cancellation path to release the transferred resource.
func (p *Prepared) Close() error {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	p.session.cancelFn()
	err := p.session.rpc.Close()
	p.plan.Cleanup()
	return err
}

// Cancel fences Start and cancels the resource even after transfer.
func (p *Prepared) Cancel(ctx context.Context) error {
	p.mu.Lock()
	p.closed = true
	started := p.started
	p.mu.Unlock()
	if started {
		if err := p.session.Cancel(ctx); err != nil {
			return err
		}
		select {
		case <-p.session.waitDone:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return p.Close()
}

// CancellationOutcome returns observed state, not a guarantee of final output or quiescence.
func (p *Prepared) CancellationOutcome() proto.DonePayload {
	return p.session.CancellationOutcome()
}

func (p *Prepared) watchOwner() {
	select {
	case <-p.session.cancelCtx.Done():
	case <-p.session.rpc.Done():
	case <-p.transferred:
		return
	}
	_ = p.Close()
}
