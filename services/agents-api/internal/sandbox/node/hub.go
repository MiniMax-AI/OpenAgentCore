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
	reservations map[string]struct{}
	closed       bool
}

type peer struct {
	identity Identity
	id       string
	epoch    uint64
	conn     *websocket.Conn
	send     sync.Mutex
	mu       sync.Mutex
	sequence uint64
	ready    bool
	pending  map[string]chan response
	done     chan struct{}
	once     sync.Once
}

func NewHub(options HubOptions) *Hub {
	return &Hub{options: options, peers: map[string]*peer{}, reservations: map[string]struct{}{}}
}
func (h *Hub) Online(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.peers[id]
	if p == nil {
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
	defer h.mu.Unlock()
	h.closed = true
	for _, p := range h.peers {
		p.close()
	}
}
func (h *Hub) Disconnect(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if p := h.peers[id]; p != nil {
		p.close()
	}
}
func (p *peer) close() { p.once.Do(func() { close(p.done); _ = p.conn.Close() }) }

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
	identity, err := h.options.Authenticate(r.Context(), id, auth[1])
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
	epoch, err := h.options.OwnerEpoch(r.Context())
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
	h.reservations[id] = struct{}{}
	h.mu.Unlock()
	// Registered first, this runs after peer removal and the fenced disconnect
	// callback. A duplicate cannot replace a connection still being retired.
	defer func() { h.mu.Lock(); delete(h.reservations, id); h.mu.Unlock() }()
	upgrade := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}
	conn, err := upgrade.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(MaxFrameBytes)
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	hello, err := readFrame(conn)
	if err != nil || hello.Type != "hello" || hello.Identity == nil || !sameBackend(*hello.Identity, identity) || hello.Health == nil {
		_ = conn.Close()
		return
	}
	p := &peer{identity: identity, id: uuid.NewString(), epoch: epoch, conn: conn, pending: map[string]chan response{}, ready: hello.Health.ProviderReady, done: make(chan struct{})}
	if writeFrame(conn, frame{Type: "welcome", ConnectionID: p.id, OwnerEpoch: epoch}) != nil {
		p.close()
		return
	}
	defer func() {
		p.close()
		h.mu.Lock()
		if h.peers[id] == p {
			delete(h.peers, id)
		}
		h.mu.Unlock()
		if h.options.Disconnected != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			h.options.Disconnected(ctx, identity, p.id, p.epoch)
		}
	}()
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		p.close()
		return
	}
	if h.options.Connected != nil {
		err = h.options.Connected(r.Context(), identity, p.id, p.epoch)
	}
	if err != nil {
		h.mu.Unlock()
		p.close()
		return
	}
	if h.options.Heartbeat != nil {
		err = h.options.Heartbeat(r.Context(), identity, p.id, p.epoch, *hello.Health)
		if err != nil {
			h.mu.Unlock()
			p.close()
			return
		}
	}
	h.peers[id] = p
	h.mu.Unlock()

	for {
		_ = conn.SetReadDeadline(time.Now().Add(35 * time.Second))
		f, e := readFrame(conn)
		if e != nil {
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
			fresh, e := h.options.Authenticate(r.Context(), id, auth[1])
			if e != nil || !sameBackend(fresh, identity) {
				return
			}
			current, e := h.options.OwnerEpoch(r.Context())
			if e != nil || current != epoch {
				return
			}
			if h.options.Heartbeat != nil && h.options.Heartbeat(r.Context(), identity, p.id, p.epoch, *f.Health) != nil {
				return
			}
			p.mu.Lock()
			p.ready = f.Health.ProviderReady
			p.mu.Unlock()
			p.send.Lock()
			e = writeFrame(conn, frame{Type: "heartbeat_ack", ConnectionID: p.id})
			p.send.Unlock()
			if e != nil {
				return
			}
		default:
			return
		}
	}
}

func sameBackend(a, b Identity) bool {
	return a.NodeID == b.NodeID && a.InstallationID == b.InstallationID && a.Provider == b.Provider && a.BackendFingerprint == b.BackendFingerprint
}

func (h *Hub) call(ctx context.Context, id string, q request) (response, error) {
	if err := ctx.Err(); err != nil {
		return response{}, uncertain(q.Operation, err)
	}
	epoch, err := h.options.OwnerEpoch(ctx)
	if err != nil {
		return response{}, uncertain(q.Operation, err)
	}
	h.mu.Lock()
	p := h.peers[id]
	h.mu.Unlock()
	if p == nil || p.epoch != epoch {
		return response{}, uncertain(q.Operation, ErrUnavailable)
	}
	p.mu.Lock()
	ready := p.ready
	p.mu.Unlock()
	if requiresReady(q) && !ready {
		return response{}, uncertain(q.Operation, ErrUnavailable)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
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
	p.send.Lock()
	select {
	case <-p.done:
		err = ErrUnavailable
	default:
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if err = q.setTimeout(deadline); err == nil {
			p.sequence++
			q.Sequence = p.sequence
			err = writeFrame(p.conn, frame{Type: "request", Request: &q})
		}
	}
	p.send.Unlock()
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

type Resolver func(context.Context, sandbox.Reference) (string, error)
