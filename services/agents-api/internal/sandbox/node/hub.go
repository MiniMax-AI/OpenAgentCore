package node

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type HubOptions struct {
	Generations  func(context.Context, Identity, string, uint64, Health) error
	Retention    func(context.Context, Identity, string, uint64, []sandbox.GenerationReference) (sandbox.NodeDeployment, []sandbox.GenerationRetention, error)
	Authenticate func(context.Context, string, string) (Identity, error)
	OwnerEpoch   func(context.Context) (uint64, error)
	Connected    func(context.Context, Identity, string, uint64) error
	Disconnected func(context.Context, Identity, string, uint64)
	Heartbeat    func(context.Context, Identity, string, uint64, Health) error
}

type Hub struct {
	options HubOptions
	mu      sync.Mutex
	peers   map[string]*peer
	// reservations cover opening, live and closing connections for one identity.
	reservations map[string]*connectionLifetime
	closed       bool
	ctx          context.Context
	cancel       context.CancelFunc
}

type peer struct {
	generationManagement bool
	version              int
	generations          map[uint64]sandbox.GenerationStatus
	controlSequence      uint64
	identity             Identity
	id                   string
	epoch                uint64
	conn                 *websocket.Conn
	send                 chan struct{}
	mu                   sync.Mutex
	sequence             uint64
	ready                bool
	pending              map[string]chan response
	done                 chan struct{}
	once                 sync.Once
	cancel               context.CancelFunc
}

func NewHub(options HubOptions) *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	return &Hub{options: options, peers: map[string]*peer{}, reservations: map[string]*connectionLifetime{}, ctx: ctx, cancel: cancel}
}
func (h *Hub) Online(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.peers[id]
	if h.closed || p == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	// Cancellation reaches authentication, opening sockets and live callbacks.
	// It does not wait for a database callback or run socket I/O under h.mu.
	h.cancel()
}
func (h *Hub) Disconnect(id string) {
	h.mu.Lock()
	lifetime := h.reservations[id]
	h.mu.Unlock()
	if lifetime != nil {
		lifetime.cancel()
	}
}
func (p *peer) close() { p.once.Do(func() { close(p.done); p.cancel(); _ = p.conn.Close() }) }

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || h.options.Authenticate == nil || h.options.OwnerEpoch == nil {
		http.Error(w, "node unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.URL.Query().Get("node_id")
	auth := strings.Fields(r.Header.Get("Authorization"))
	if !validID(id) || len(r.Header.Values("Authorization")) != 1 || len(auth) != 2 || auth[0] != "Bearer" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx, cancel := h.lifetime(r.Context())
	defer cancel()
	identity, err := callbackValue(ctx, func(ctx context.Context) (Identity, error) { return h.options.Authenticate(ctx, id, auth[1]) })
	if err != nil {
		if errors.Is(err, ErrAuthentication) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		} else {
			http.Error(w, "node authentication unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	if identity.NodeID != id {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	epoch, err := callbackValue(ctx, h.options.OwnerEpoch)
	if err != nil || epoch == 0 {
		http.Error(w, "owner unavailable", http.StatusServiceUnavailable)
		return
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		http.Error(w, "node unavailable", http.StatusServiceUnavailable)
		return
	}
	if _, reserved := h.reservations[id]; reserved {
		h.mu.Unlock()
		http.Error(w, "node identity already connected", http.StatusConflict)
		return
	}
	lifetime := &connectionLifetime{cancel: cancel}
	h.reservations[id] = lifetime
	h.mu.Unlock()
	// Registered first, this runs after peer removal and the fenced disconnect
	// callback. A duplicate cannot replace a connection still being retired.
	defer func() {
		h.mu.Lock()
		if h.reservations[id] == lifetime {
			delete(h.reservations, id)
		}
		h.mu.Unlock()
	}()
	upgrade := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}
	conn, err := upgrade.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	defer conn.Close()
	conn.SetReadLimit(MaxFrameBytes)
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	hello, err := readFrame(conn)
	if err != nil || hello.Type != "hello" || hello.Identity == nil || !sameBackend(*hello.Identity, identity) || hello.Health == nil {
		_ = conn.Close()
		return
	}
	p := &peer{generationManagement: hello.GenerationManagement, version: hello.Version, generations: map[uint64]sandbox.GenerationStatus{}, identity: identity, id: uuid.NewString(), epoch: epoch, conn: conn, cancel: cancel, send: make(chan struct{}, 1), pending: map[string]chan response{}, ready: hello.Health.ProviderReady, done: make(chan struct{})}
	stopPeerClose := context.AfterFunc(ctx, p.close)
	defer stopPeerClose()
	presenceAttempted := false
	defer func() {
		p.close()
		h.mu.Lock()
		if h.peers[id] == p {
			delete(h.peers, id)
		}
		h.mu.Unlock()
		if presenceAttempted && h.options.Disconnected != nil {
			// Even an errored Connected may have committed. Cleanup must outlive
			// connection/Hub cancellation, and retain the identity reservation.
			_ = callback(context.Background(), func(ctx context.Context) error {
				h.options.Disconnected(ctx, identity, p.id, p.epoch)
				return nil
			})
		}
	}()
	if h.options.Connected != nil {
		presenceAttempted = true
		err = callback(ctx, func(ctx context.Context) error {
			return h.options.Connected(ctx, identity, p.id, p.epoch)
		})
		if err != nil {
			return
		}
	}
	if err = h.recordHealth(ctx, p, *hello.Health); err != nil {
		return
	}
	welcome := frame{Version: p.version, Type: "welcome", ConnectionID: p.id, OwnerEpoch: epoch}
	if p.generationManagement {
		deployment, _, err := h.retention(ctx, p, nil)
		if err != nil {
			return
		}
		welcome.Deployment = &deployment
	}
	if ctx.Err() != nil || writeFrame(conn, welcome) != nil {
		return
	}
	h.mu.Lock()
	if h.closed || ctx.Err() != nil {
		h.mu.Unlock()
		return
	}
	h.peers[id] = p
	h.mu.Unlock()

	for {
		_ = conn.SetReadDeadline(time.Now().Add(35 * time.Second))
		f, e := readFrame(conn)
		if e != nil || f.Version != p.version {
			return
		}
		switch f.Type {
		case "response":
			if f.Response == nil || f.Response.ConnectionID != p.id {
				return
			}
			p.mu.Lock()
			ch := p.pending[f.Response.ID]
			p.mu.Unlock()
			if ch != nil {
				select {
				case ch <- *f.Response:
				default:
				}
			}
		case "heartbeat":
			if f.ConnectionID != p.id || f.Health == nil {
				return
			}
			fresh, e := callbackValue(ctx, func(ctx context.Context) (Identity, error) { return h.options.Authenticate(ctx, id, auth[1]) })
			if e != nil || !sameBackend(fresh, identity) {
				return
			}
			current, e := callbackValue(ctx, h.options.OwnerEpoch)
			if e != nil || current != epoch {
				return
			}
			if f.OwnerEpoch != p.epoch {
				return
			}
			if h.recordHealth(ctx, p, *f.Health) != nil {
				return
			}
			if e = p.lockSend(ctx); e != nil {
				return
			}
			ack := frame{Version: p.version, Type: "heartbeat_ack", ConnectionID: p.id, OwnerEpoch: p.epoch}
			if p.generationManagement {
				deployment, _, readErr := h.retention(ctx, p, nil)
				if readErr != nil {
					p.unlockSend()
					return
				}
				ack.Deployment = &deployment
			}
			e = writeFrame(conn, ack)
			p.unlockSend()
			if e != nil {
				return
			}
		case "retention":
			if !p.generationManagement || f.Control == nil {
				return
			}
			c := f.Control
			if c.ConnectionID != p.id || c.OwnerEpoch != p.epoch || c.Sequence != p.controlSequence+1 {
				return
			}
			p.controlSequence = c.Sequence
			deployment, grants, readErr := h.retention(ctx, p, c.References)
			if readErr != nil {
				return
			}
			ack := frame{Version: p.version, Type: "retention_ack", Deployment: &deployment, Control: &generationControl{ID: c.ID, Sequence: c.Sequence, ConnectionID: p.id, OwnerEpoch: p.epoch, Retentions: grants}}
			if err := p.lockSend(ctx); err != nil {
				return
			}
			err := writeFrame(conn, ack)
			p.unlockSend()
			if err != nil {
				return
			}
			p.mu.Lock()
			for _, g := range grants {
				if !g.Keep {
					delete(p.generations, g.Generation)
				}
			}
			p.mu.Unlock()
		default:
			return
		}
	}
}

func sameBackend(a, b Identity) bool {
	return a.SpecificationDigest == b.SpecificationDigest && a.DeploymentGeneration == b.DeploymentGeneration && a.NodeID == b.NodeID && a.InstallationID == b.InstallationID && a.Provider == b.Provider && a.BackendFingerprint == b.BackendFingerprint
}

func (h *Hub) call(ctx context.Context, id string, q request) (response, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	ctx, cancel := h.lifetime(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return response{}, uncertain(q.Operation, err)
	}
	epoch, err := callbackValue(ctx, h.options.OwnerEpoch)
	if err != nil {
		return response{}, uncertain(q.Operation, err)
	}
	h.mu.Lock()
	p := h.peers[id]
	if h.closed {
		p = nil
	}
	h.mu.Unlock()
	if p == nil || p.epoch != epoch {
		return response{}, uncertain(q.Operation, ErrUnavailable)
	}
	p.mu.Lock()
	ready := p.ready
	if p.generationManagement {
		status, ok := p.generations[q.DeploymentGeneration]
		ready = ok && status.State == "ready"
	} else if q.DeploymentGeneration != p.identity.DeploymentGeneration {
		p.mu.Unlock()
		return response{}, uncertain(q.Operation, ErrUnavailable)
	}
	p.mu.Unlock()
	if requiresReady(q) && !ready {
		return response{}, uncertain(q.Operation, ErrUnavailable)
	}

	deadline, _ := ctx.Deadline()
	q.ID, q.ConnectionID, q.OwnerEpoch = uuid.NewString(), p.id, p.epoch
	if err = q.setTimeout(deadline); err != nil {
		return response{}, err
	}
	ch := make(chan response, 1)
	p.mu.Lock()
	if len(p.pending) >= maxPending {
		p.mu.Unlock()
		return response{}, uncertain(q.Operation, ErrUnavailable)
	}
	p.pending[q.ID] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, q.ID); p.mu.Unlock() }()
	if err = p.lockSend(ctx); err != nil {
		return response{}, uncertain(q.Operation, err)
	}
	select {
	case <-p.done:
		err = ErrUnavailable
	default:
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if err = q.setTimeout(deadline); err == nil {
			p.sequence++
			q.Sequence = p.sequence
			err = writeFrame(p.conn, frame{Version: p.version, Type: "request", Request: &q})
		}
	}
	p.unlockSend()
	if err != nil {
		p.close()
		return response{}, uncertain(q.Operation, err)
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case result := <-ch:
		return result, responseError(result.ErrorCode)
	case <-p.done:
		return response{}, uncertain(q.Operation, ErrUnavailable)
	case <-ctx.Done():
		return response{}, uncertain(q.Operation, ctx.Err())
	case <-timer.C:
		return response{}, uncertain(q.Operation, context.DeadlineExceeded)
	}
}

func (h *Hub) recordHealth(ctx context.Context, p *peer, health Health) error {
	if !p.generationManagement && health.Generations != nil {
		return sandbox.ErrInvalid
	}
	if p.generationManagement {
		if h.options.Generations == nil {
			return sandbox.ErrInvalid
		}
		if err := callback(ctx, func(ctx context.Context) error { return h.options.Generations(ctx, p.identity, p.id, p.epoch, health) }); err != nil {
			return err
		}
		refs := make([]sandbox.GenerationReference, 0, len(health.Generations))
		for _, g := range health.Generations {
			refs = append(refs, sandbox.GenerationReference{Generation: g.Generation, SpecificationDigest: g.SpecificationDigest})
		}
		_, grants, err := h.retention(ctx, p, refs)
		if err != nil {
			return err
		}
		if len(grants) != len(refs) {
			return sandbox.ErrInvalid
		}
		for i, grant := range grants {
			if grant.GenerationReference != refs[i] {
				return sandbox.ErrInvalid
			}
		}
		p.mu.Lock()
		for i, g := range health.Generations {
			if grants[i].Keep {
				p.generations[g.Generation] = g
			} else {
				delete(p.generations, g.Generation)
			}
		}
		p.mu.Unlock()
		return nil
	}
	if h.options.Heartbeat != nil {
		if err := callback(ctx, func(ctx context.Context) error { return h.options.Heartbeat(ctx, p.identity, p.id, p.epoch, health) }); err != nil {
			return err
		}
	}
	p.mu.Lock()
	p.ready = health.ProviderReady
	p.mu.Unlock()
	return nil
}
func (h *Hub) retention(ctx context.Context, p *peer, refs []sandbox.GenerationReference) (sandbox.NodeDeployment, []sandbox.GenerationRetention, error) {
	if h.options.Retention == nil {
		return sandbox.NodeDeployment{}, nil, sandbox.ErrInvalid
	}
	type result struct {
		deployment sandbox.NodeDeployment
		grants     []sandbox.GenerationRetention
	}
	value, err := callbackValue(ctx, func(ctx context.Context) (result, error) {
		d, g, err := h.options.Retention(ctx, p.identity, p.id, p.epoch, refs)
		return result{d, g}, err
	})
	return value.deployment, value.grants, err
}
