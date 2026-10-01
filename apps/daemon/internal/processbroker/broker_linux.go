//go:build linux

package processbroker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// Broker serves one Session's shim invocations, which its relay hands it.
type Broker struct {
	cfg  Config
	log  *slog.Logger
	conn *net.UnixConn
	link *link

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup // the reader and the invocations
	sendMu sync.Mutex
	done   chan struct{}

	mu      sync.Mutex
	invs    map[uint64]*invocation
	lastID  uint64
	closing bool
	err     error

	closeOnce sync.Once
}

// Start serves the relay at cfg.Relay until Close or the relay's loss.
func Start(cfg Config) (*Broker, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	c, err := net.FileConn(cfg.Relay)
	if err != nil {
		return nil, fmt.Errorf("processbroker: relay connection: %w", err)
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		c.Close()
		return nil, fmt.Errorf("%w: the relay connection is not a Unix socket", ErrInvalidConfig)
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &Broker{
		cfg: cfg, log: log, conn: uc, link: newLink(cfg.Dial, log),
		ctx: ctx, cancel: cancel, done: make(chan struct{}), invs: map[uint64]*invocation{},
	}
	b.wg.Add(1)
	go b.read()
	return b, nil
}

// Close ends every invocation and the relay connection, and waits for the
// invocations. It never waits on the relay: the relay answers a waiting shim
// with 255. Remote operations are left to the Session.
func (b *Broker) Close() error {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closing = true
		b.mu.Unlock()
		b.cancel()
		b.conn.CloseWrite()
		b.conn.Close()
		b.wg.Wait()
		b.link.close()
	})
	return nil
}

// Done closes when the broker stops serving: after Close, or when the relay
// is lost.
func (b *Broker) Done() <-chan struct{} { return b.done }

// Err returns an error wrapping ErrRelayLost once the relay was lost before
// Close, and nil otherwise.
func (b *Broker) Err() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

// read dispatches the relay's messages until the connection ends or breaks
// the IPC. It reads without a control buffer, so the kernel closes any
// descriptor the relay attaches, and it never blocks on an invocation.
func (b *Broker) read() {
	defer b.wg.Done()
	r := bufio.NewReaderSize(b.conn, 64<<10)
	var err error
	for err == nil {
		var f sandboxwire.Frame
		if f, err = sandboxwire.ReadFrame(r, processshim.MaxFrameBytes); err != nil {
			break
		}
		var m processshim.RelayMessage
		if m, err = processshim.DecodeRelay(f); err == nil {
			err = b.dispatch(m)
		}
	}
	b.mu.Lock()
	lost := !b.closing
	if lost {
		b.err = fmt.Errorf("%w: %w", ErrRelayLost, err)
	}
	b.mu.Unlock()
	if lost {
		b.log.Error("process relay lost", "error", err)
	}
	b.cancel()
	b.conn.Close()
	close(b.done)
}

func (b *Broker) dispatch(m processshim.RelayMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := m.Invocation()
	if open, ok := m.(processshim.Open); ok {
		switch {
		case id <= b.lastID:
			return fmt.Errorf("%w: open of invocation %d after %d", processshim.ErrProtocol, id, b.lastID)
		case len(b.invs) >= processshim.MaxInvocations:
			return fmt.Errorf("%w: more than %d invocations", processshim.ErrProtocol, processshim.MaxInvocations)
		}
		b.lastID = id
		inv := b.newInvocation(open)
		b.invs[id] = inv
		b.wg.Add(1)
		go inv.serve()
		return nil
	}
	inv := b.invs[id]
	switch {
	case inv != nil:
		return inv.receive(m)
	case id > b.lastID:
		return fmt.Errorf("%w: message for unopened invocation %d", processshim.ErrProtocol, id)
	}
	return nil // an invocation that has ended
}

// unregister stops dispatching to the invocation.
func (b *Broker) unregister(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.invs, id)
}

var errEnded = errors.New("invocation ended")

func (b *Broker) send(m processshim.BrokerMessage) error {
	b.sendMu.Lock()
	defer b.sendMu.Unlock()
	return sandboxwire.WriteFrame(b.conn, processshim.Frame(m))
}
