package node

import (
	"context"
	"errors"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/providers"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/gorilla/websocket"
)

type AgentConfig struct {
	Generations    *GenerationManager
	CoreURL        string
	StateDirectory string
	Identity       Identity
	Credential     string
	Provider       sandbox.SandboxProvider
	Probe          func(context.Context) (Health, error)
	// Dialer is optional, primarily for an operator-supplied TLS trust configuration.
	Dialer *websocket.Dialer
}

type agent struct {
	config     AgentConfig
	stored     StoredIdentity
	mu         sync.Mutex
	current    *agentConnection
	queue      chan work
	active     atomic.Int32
	ready      atomic.Bool
	healthSeen atomic.Bool
}
type agentConnection struct {
	controlBusy                    bool
	version                        int
	controls                       sync.Mutex
	pending                        *generationControl
	controlSequence, controlCursor uint64
	conn                           *websocket.Conn
	id                             string
	epoch                          uint64
	send                           sync.Mutex
	done                           chan struct{}
	once                           sync.Once
}

func (c *agentConnection) close() { c.once.Do(func() { close(c.done); _ = c.conn.Close() }) }
func (c *agentConnection) write(f frame) error {
	c.send.Lock()
	defer c.send.Unlock()
	select {
	case <-c.done:
		return ErrUnavailable
	default:
		f.Version = c.version
		return writeFrame(c.conn, f)
	}
}

type work struct {
	request    request
	connection *agentConnection
	provider   sandbox.SandboxProvider
	release    func()
	ready      bool
}

// Run owns one persistent node identity and reconnects its transport only. It
// never resends a Provider request. The bounded worker outlives each connection.
func Run(ctx context.Context, config AgentConfig) error {
	if config.Generations == nil && (config.Provider == nil || config.Probe == nil) {
		return sandbox.ErrInvalid
	}
	if config.Generations == nil {
		if err := sandbox.ValidateProvider(config.Provider); err != nil {
			return err
		}
		if sandbox.SupportsCheckpoint(config.Provider) != providers.SupportsCheckpoint(config.Identity.Provider) {
			return sandbox.ErrInvalid
		}
	}
	release, err := lockDirectory(config.StateDirectory)
	if err != nil {
		return err
	}
	defer release()
	stored, err := readIdentity(config.StateDirectory)
	if err != nil {
		return err
	}
	if !sameBackend(config.Identity, stored.Identity) || config.Credential != stored.Credential {
		return sandbox.ErrOwnership
	}
	if config.CoreURL == "" {
		config.CoreURL = stored.CoreURL
	}
	if config.CoreURL != stored.CoreURL {
		return sandbox.ErrOwnership
	}
	if _, err = endpoint(config.CoreURL, ""); err != nil {
		return err
	}
	a := &agent{config: config, stored: stored, queue: make(chan work, maxPending)}
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); a.worker(workerCtx) }()
	defer func() {
		stopWorker()
		a.mu.Lock()
		if a.current != nil {
			a.current.close()
		}
		a.mu.Unlock()
		<-workerDone
	}()
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return nil
		}
		connectedAt := time.Now()
		err = a.connect(ctx)
		if errors.Is(err, ErrAuthentication) || errors.Is(err, sandbox.ErrOwnership) {
			return err
		}
		if ctx.Err() == nil {
			log.Ctx(ctx).Warn("sandbox node connection interrupted; reconnecting", "node_id", config.Identity.NodeID)
		}
		if time.Since(connectedAt) >= 30*time.Second {
			backoff = time.Second
		}
		delay := backoff/2 + time.Duration(rand.Int64N(int64(backoff/2)+1))
		timer := time.NewTimer(delay)
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
func (a *agent) health(ctx context.Context, host *hostHealthSampler) (Health, error) {
	if a.config.Generations != nil {
		health := Health{Generations: a.config.Generations.Statuses(), ObservedAt: time.Now().UTC(), ActiveOperations: int(a.active.Load())}
		host.fill(&health, a.config.StateDirectory)
		return health, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	h, e := a.config.Probe(probeCtx)
	h.ProviderReady = e == nil
	// Only the fixed code leaves this process; the probe error may name host paths.
	h.Diagnostic = sandbox.NodeDiagnostic(e)
	if e != nil && ctx.Err() != nil {
		// A closing connection cancelled the probe; that says nothing about the provider.
		h.Diagnostic = sandbox.NodeProviderUnavailable
	} else {
		wasReady := a.ready.Swap(h.ProviderReady)
		seen := a.healthSeen.Swap(true)
		if e != nil && probeCtx.Err() == nil && (!seen || wasReady) {
			// The local error stays in this host's journal; it may name host paths.
			log.Ctx(ctx).Warn("sandbox node provider unavailable; check local runtime configuration and permissions", "node_id", a.config.Identity.NodeID, "diagnostic", h.Diagnostic, "error", e)
		}
	}
	h.ObservedAt = time.Now().UTC()
	h.ActiveOperations = int(a.active.Load())
	host.fill(&h, a.config.StateDirectory)
	return h, e
}
func (a *agent) connect(ctx context.Context) error {
	endpointURL, err := endpoint(a.config.CoreURL, "/api/v1/sandbox-node/connect")
	if err != nil {
		return err
	}
	endpointURL = strings.Replace(endpointURL, "https://", "wss://", 1)
	endpointURL = strings.Replace(endpointURL, "http://", "ws://", 1)
	endpointURL += "?node_id=" + url.QueryEscape(a.config.Identity.NodeID)
	dialer := a.config.Dialer
	if dialer == nil {
		copy := *websocket.DefaultDialer
		copy.HandshakeTimeout = 10 * time.Second
		dialer = &copy
	}
	conn, resp, err := dialer.DialContext(ctx, endpointURL, http.Header{"Authorization": []string{"Bearer " + a.config.Credential}})
	if err != nil {
		if resp != nil {
			_ = resp.Body.Close()
			// Only Core's 401 rejects the credential for good; a 403 may come from a
			// proxy or firewall in front of Core, so the node keeps retrying.
			if resp.StatusCode == http.StatusUnauthorized {
				return ErrAuthentication
			}
		}
		return ErrUnavailable
	}
	defer conn.Close()
	conn.SetReadLimit(MaxFrameBytes)
	host := new(hostHealthSampler)
	health, _ := a.health(ctx, host)
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	version := ProtocolVersion
	if err = writeFrame(conn, frame{Version: version, Type: "hello", GenerationManagement: a.config.Generations != nil, Identity: &a.config.Identity, Health: &health}); err != nil {
		return err
	}
	welcome, err := readFrame(conn)
	if err != nil {
		return err
	}
	if (a.config.Generations == nil && welcome.Deployment != nil) || welcome.Version != version || welcome.Type != "welcome" || !validID(welcome.ConnectionID) || welcome.OwnerEpoch == 0 {
		return sandbox.ErrInvalid
	}
	a.mu.Lock()
	if welcome.OwnerEpoch < a.stored.OwnerEpoch {
		a.mu.Unlock()
		return sandbox.ErrOwnership
	}
	if welcome.OwnerEpoch > a.stored.OwnerEpoch {
		a.stored.OwnerEpoch = welcome.OwnerEpoch
		if err = writeIdentity(a.config.StateDirectory, a.stored); err != nil {
			a.mu.Unlock()
			return err
		}
	}
	current := &agentConnection{version: version, conn: conn, id: welcome.ConnectionID, epoch: welcome.OwnerEpoch, done: make(chan struct{})}
	a.current = current
	a.mu.Unlock()
	defer current.close()
	connectionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	detach := context.AfterFunc(connectionCtx, current.close)
	defer detach()
	go a.heartbeats(connectionCtx, current, host)
	var controlDone chan struct{}
	controlWork := make(chan []sandbox.GenerationRetention, 1)
	if a.config.Generations != nil {
		if welcome.Deployment == nil {
			return sandbox.ErrInvalid
		}
		if err := a.config.Generations.Deployment(*welcome.Deployment); err != nil {
			return err
		}
		controlDone = make(chan struct{})
		go func() { defer close(controlDone); a.garbageCollection(connectionCtx, current, controlWork) }()
		defer func() { cancel(); <-controlDone }()
	}
	sequence := uint64(0)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(35 * time.Second))
		f, e := readFrame(conn)
		if e != nil || f.Version != version {
			if e != nil {
				return e
			}
			return sandbox.ErrInvalid
		}
		if f.Type == "heartbeat_ack" && f.ConnectionID == current.id {
			if f.OwnerEpoch != current.epoch || (a.config.Generations == nil && f.Deployment != nil) {
				return sandbox.ErrOwnership
			}
			if a.config.Generations != nil {
				if f.Deployment == nil {
					return sandbox.ErrInvalid
				}
				if err := a.config.Generations.Deployment(*f.Deployment); err != nil {
					return err
				}
			}
			continue
		}
		if f.Type == "retention_ack" && a.config.Generations != nil {
			if err := a.acceptRetention(current, f, controlWork); err != nil {
				return err
			}
			continue
		}
		if f.Type != "request" || f.Request == nil {
			return sandbox.ErrInvalid
		}
		q := *f.Request
		if a.config.Generations == nil && q.DeploymentGeneration != a.config.Identity.DeploymentGeneration {
			return sandbox.ErrOwnership
		}
		if q.ConnectionID != current.id || q.OwnerEpoch != current.epoch || q.Sequence != sequence+1 {
			return sandbox.ErrOwnership
		}
		sequence = q.Sequence
		if q.receive(time.Now()) != nil {
			_ = current.write(frame{Type: "response", Response: &response{ID: q.ID, ConnectionID: current.id, ErrorCode: "invalid"}})
			continue
		}
		task := work{request: q, connection: current, provider: a.config.Provider, ready: a.ready.Load()}
		if a.config.Generations != nil {
			task.provider, task.ready, task.release, err = a.config.Generations.Acquire(q.DeploymentGeneration)
			if err != nil {
				_ = current.write(frame{Type: "response", Response: &response{ID: q.ID, ConnectionID: current.id, ErrorCode: "unconfirmed"}})
				continue
			}
		}
		select {
		case a.queue <- task:
		default:
			if task.release != nil {
				task.release()
			}
			return ErrUnavailable
		}
	}
}
func (a *agent) heartbeats(ctx context.Context, c *agentConnection, host *hostHealthSampler) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-ticker.C:
			h, _ := a.health(ctx, host)
			if c.write(frame{Type: "heartbeat", ConnectionID: c.id, OwnerEpoch: c.epoch, Health: &h}) != nil {
				c.close()
				return
			}
			if a.config.Generations != nil && a.requestRetention(c) != nil {
				c.close()
				return
			}
		}
	}
}
func (a *agent) worker(ctx context.Context) {
	defer func() {
		for {
			select {
			case task := <-a.queue:
				if task.release != nil {
					task.release()
				}
			default:
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-a.queue:
			a.executeWork(ctx, task)
		}
	}
}
func (a *agent) executeWork(ctx context.Context, task work) {
	if task.release != nil {
		defer task.release()
	}
	a.mu.Lock()
	current := a.current
	a.mu.Unlock()
	if current != task.connection || ctx.Err() != nil {
		return
	}
	select {
	case <-task.connection.done:
		return
	default:
	}
	ready := a.ready.Load()
	if a.config.Generations != nil {
		ready = a.config.Generations.Ready(task.request.DeploymentGeneration)
	}
	if !time.Now().Before(task.request.deadline) || requiresReady(task.request) && !ready {
		_ = task.connection.write(frame{Type: "response", Response: &response{ID: task.request.ID, ConnectionID: task.connection.id, ErrorCode: "unconfirmed"}})
		return
	}
	// Detached native completion retains its local provider reference across cancellation.
	operationCtx, cancel := context.WithDeadline(context.Background(), task.request.deadline)
	a.active.Add(1)
	result := execute(operationCtx, task.provider, task.request)
	a.active.Add(-1)
	cancel()
	_ = task.connection.write(frame{Type: "response", Response: &result})
}
