// Package dispatch wires inbound WebSocket frames to the agent layer.
// It owns the Session assignments of its connection, one prepared run per
// active RunID with a goroutine that forwards the run's events to the
// transport, and a permission_id → run_id index so permission_decision
// frames route back to the right run.
//
// Concurrency: Handle is safe for one goroutine (typically the read
// loop). Each run has its own goroutines. Internal state is
// mutex-protected.
package dispatch

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

// Sender is the subset of transport.Conn the dispatcher needs. Tests
// supply a fake; production wires this to *transport.Conn.Send.
type Sender interface {
	Send(ctx context.Context, env proto.Envelope) error
}

// Router maps inbound Envelopes to agent sessions.
type Router struct {
	registry *agent.Registry
	sender   Sender
	log      *slog.Logger

	admission           sync.RWMutex
	suspension          *proto.EnvironmentSuspendPayload
	suspendedBy         proto.AssignmentRef // the assignment that quiesced
	mu                  sync.Mutex
	assignments         map[string]*assignmentState // SessionID → assignment
	sessions            map[string]*sessionState    // RunID → state
	permIndex           map[string]string           // permID  → RunID
	askIndex            map[string]string           // askID   → RunID
	applied             map[string]appliedInteractionDecision
	shutdownAttempt     *shutdownAttempt
	shutdownCh          chan struct{} // closed by Shutdown
	shutdownWG          dispatchWork  // waits for all pump goroutines
	idleTimeout         time.Duration
	closed              bool
	executors           map[string]*executorState
	preparations        map[string]*preparationState
	preparationRequests map[string]*preparationState
	preparationTimeout  time.Duration
	runtimePreparation  *runtimePreparationTransfer
	workspaceWrite      *workspaceUpload
	workspaceExport     *workspaceExport
	workspaceReads      map[string]workspaceRead
	localWorkspace      *localworkspace.Binding
	sessionEnvironments bool
	removeHome          func(sessionID string) error
}

type appliedInteractionDecision struct {
	requestID   string
	kind        string
	fingerprint [32]byte
	assignment  proto.AssignmentRef
	recordedAt  time.Time
}

// sessionState is the dispatcher's per-run bookkeeping. The agent
// owns the close of out; the dispatcher cancels ctxCancel to wind
// down. traceparent captures the execution_start's W3C trace so every
// outbound frame stamps env.Trace with the same value, completing
// frontend → server → daemon → agent → server attribution.
type sessionState struct {
	assignment      proto.AssignmentRef
	capabilities    proto.AgentKindCapabilities
	runID           string
	environmentID   string
	session         agent.Session
	out             chan proto.Envelope
	ctx             context.Context
	ctxCancel       context.CancelFunc
	pendingIDs      map[string]struct{}
	pendingAsks     map[string]struct{}
	traceparent     string
	steering        map[string]steeringReceipt
	steerBusy       bool
	steeringClosed  bool
	preparedHandoff *preparedHandoff
}

// Config is the constructor input. Registry and Sender are required;
// Log is optional (defaults to slog.Default()).
type Config struct {
	Registry           *agent.Registry
	Sender             Sender
	Log                *slog.Logger
	IdleTimeout        time.Duration
	PreparationTimeout time.Duration
	LocalWorkspace     *localworkspace.Binding
	// SessionEnvironments says that a prepared execution's LocalEnvironment
	// is its Session's Environment, which the Executor factory binds, and not
	// a local workspace of this daemon. It excludes LocalWorkspace.
	SessionEnvironments bool
	// RemoveHome removes the Session's native home once its Executors have
	// closed. Nil declares that assignment_release does not accept RemoveHome.
	RemoveHome func(sessionID string) error
}

const defaultIdleTimeout = time.Hour

// New returns a Router ready to Handle inbound frames.
func New(cfg Config) (*Router, error) {
	if cfg.Registry == nil {
		return nil, errors.New("dispatch.New: Registry is required")
	}
	if cfg.Sender == nil {
		return nil, errors.New("dispatch.New: Sender is required")
	}
	if cfg.SessionEnvironments && cfg.LocalWorkspace != nil {
		return nil, errors.New("dispatch.New: SessionEnvironments excludes LocalWorkspace")
	}
	log := cfg.Log
	if log == nil {
		log = obslog.Bg()
	}
	log = log.With("component", "dispatch")
	idleTimeout := cfg.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleTimeout
	}
	preparationTimeout := cfg.PreparationTimeout
	if preparationTimeout <= 0 || preparationTimeout > 5*time.Minute {
		preparationTimeout = 5 * time.Minute
	}
	return &Router{
		registry:            cfg.Registry,
		sender:              cfg.Sender,
		log:                 log,
		assignments:         make(map[string]*assignmentState),
		sessions:            make(map[string]*sessionState),
		permIndex:           make(map[string]string),
		askIndex:            make(map[string]string),
		applied:             make(map[string]appliedInteractionDecision),
		shutdownCh:          make(chan struct{}),
		idleTimeout:         idleTimeout,
		executors:           make(map[string]*executorState),
		preparations:        make(map[string]*preparationState),
		preparationRequests: make(map[string]*preparationState),
		preparationTimeout:  preparationTimeout,
		localWorkspace:      cfg.LocalWorkspace,
		sessionEnvironments: cfg.SessionEnvironments,
		removeHome:          cfg.RemoveHome,
	}, nil
}

// Handle dispatches one inbound Envelope. Errors are returned for
// programmer-visible problems (bad shape, registry miss); transient
// session-level failures are logged and swallowed. A quiesced Router
// still handles assignment_release.
//
// Adopts env.Trace into ctx so every downstream log under it inherits
// the same trace_id, making a single grep cover both sides.
func (r *Router) Handle(ctx context.Context, env proto.Envelope) error {
	r.admission.RLock()
	defer r.admission.RUnlock()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	if r.suspension != nil && env.Type != proto.TypeAssignmentRelease {
		r.mu.Unlock()
		return ErrRouterQuiesced
	}
	r.mu.Unlock()

	ctx = adoptEnvelopeTrace(ctx, env)

	switch env.Type {
	case proto.TypeAssignmentBind:
		return r.handleAssignmentBind(ctx, env)
	case proto.TypeAssignmentRelease:
		return r.handleAssignmentRelease(ctx, env)
	case proto.TypeRuntimePrepare:
		return r.handleRuntimePrepare(ctx, env)
	case proto.TypeWorkspaceExport:
		return r.handleWorkspaceExport(ctx, env)
	case proto.TypeWorkspaceWrite:
		return r.handleWorkspaceWrite(ctx, env)
	case proto.TypeWorkspaceRead:
		return r.handleWorkspaceRead(ctx, env)
	case proto.TypeExecutionPrepare:
		return r.handleExecutionPrepare(ctx, env)
	case proto.TypeExecutionStart:
		return r.handleExecutionStart(ctx, env)
	case proto.TypeExecutionRelease:
		return r.handleExecutionRelease(ctx, env)
	case proto.TypePromptCancel:
		return r.handlePromptCancel(ctx, env)
	case proto.TypeFunctionResult:
		return r.handleFunctionResult(ctx, env)
	case proto.TypePromptSteer:
		return r.handlePromptSteer(ctx, env)
	case proto.TypePermissionDecision:
		return r.handlePermissionDecision(ctx, env)
	case proto.TypePromptForUserChoiceDecision:
		return r.handlePromptForUserChoiceDecision(ctx, env)
	default:
		r.log.WarnContext(ctx, "unsupported envelope type", "type", env.Type, "id", env.ID)
		return r.reply(ctx, env, proto.TypeProtocolError, proto.ProtocolErrorPayload{Type: env.Type, ErrorCode: proto.UnsupportedOperation})
	}
}

// adoptEnvelopeTrace returns a ctx carrying env.Trace's carrier. On
// empty / unparseable trace we mint a fresh one rather than propagate
// a bad trace_id back to the server.
func adoptEnvelopeTrace(ctx context.Context, env proto.Envelope) context.Context {
	if env.Trace != "" {
		if carrier, err := obslog.ParseTraceparent(env.Trace); err == nil {
			return obslog.WithTrace(ctx, carrier)
		}
	}
	ctx, _ = obslog.StartBackgroundTrace(ctx, "daemon.envelope")
	return ctx
}

// ActiveRuns returns the in-flight run count. Wired into the heartbeat
// payload supplier.
func (r *Router) ActiveRuns() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}

var ErrRouterClosed = errors.New("dispatch: router closed")

// ---------------------------------------------------------------------
// per-type handlers
// ---------------------------------------------------------------------

func (r *Router) drain(ch <-chan proto.Envelope) {
	for range ch {
	}
}

// cleanupSession removes the run from the registry maps. The run's prepared
// release calls it once.
func (r *Router) cleanupSession(s *sessionState) {
	r.mu.Lock()
	delete(r.sessions, s.runID)
	for permID := range s.pendingIDs {
		delete(r.permIndex, permID)
	}
	for askID := range s.pendingAsks {
		delete(r.askIndex, askID)
	}
	r.mu.Unlock()
}
