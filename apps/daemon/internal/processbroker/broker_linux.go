//go:build linux

package processbroker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path"
	"slices"
	"sync"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
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
	blocked map[string]bool

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
		blocked: map[string]bool{},
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

// StopAliases stops only the selected frozen commands and waits for their
// sandbox scopes. The caller has drained native call admission and must not
// reconnect until success. Failed settlement leaves their admission closed.
func (b *Broker) StopAliases(ctx context.Context, aliases []string) error {
	if len(aliases) == 0 {
		return nil
	}
	if b.cfg.Scope != sp.ScopeCgroupV2 {
		return sp.Fail(sp.CodeUnsupported, sandboxwire.EffectNone, "stdio MCP requires cgroup v2 scopes")
	}
	b.mu.Lock()
	if b.closing || b.err != nil {
		b.mu.Unlock()
		return fmt.Errorf("%w: broker is ending", ErrUnsettled)
	}
	for _, alias := range aliases {
		if _, ok := b.cfg.Executables.Aliases[alias]; !ok {
			b.mu.Unlock()
			return sp.Fail(sp.CodeInvalidArgument, sandboxwire.EffectNone, "unknown process alias")
		}
		if b.blocked[alias] {
			b.mu.Unlock()
			return ErrUnsettled
		}
	}
	for _, alias := range aliases {
		b.blocked[alias] = true
	}
	var selected []*invocation
	for _, inv := range b.invs {
		if slices.Contains(aliases, inv.alias) {
			// Open already carries its complete Request. Mark it while the
			// same lock excludes new admissions, even if prepare has not run.
			inv.mu.Lock()
			inv.stopping = true
			inv.mu.Unlock()
			inv.loseShim()
			inv.helpers.Add(1) // unregister precedes teardown's Wait
			selected = append(selected, inv)
		}
	}
	b.mu.Unlock()
	for _, inv := range selected {
		go func() {
			defer inv.helpers.Done()
			inv.stopInput()
			inv.cancelRemote()
		}()
	}
	for _, inv := range selected {
		select {
		case <-inv.scopeDone:
		case <-inv.finished:
			if !inv.scopeSettled() {
				return ErrUnsettled
			}
		case <-ctx.Done():
			return fmt.Errorf("%w: %w", ErrUnsettled, ctx.Err())
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closing || b.err != nil {
		return ErrUnsettled
	}
	for _, alias := range aliases {
		delete(b.blocked, alias)
	}
	return nil
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
	if !b.closing {
		b.err = fmt.Errorf("%w: %w", ErrRelayLost, err)
	}
	b.mu.Unlock()
	b.cancel()
	b.conn.CloseWrite() // a relay the broker stops serving ends too
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
		if _, command, ok := b.cfg.Executables.resolve(string(open.Request.ExecPath), string(open.Request.Cwd)); ok && command != nil {
			inv.alias = path.Base(resolvePath(string(open.Request.ExecPath), string(open.Request.Cwd)))
			inv.stopping = b.blocked[inv.alias]
		}
		b.invs[id] = inv
		b.wg.Add(1)
		go inv.serve()
		return nil
	}
	inv := b.invs[id]
	switch {
	case inv != nil && inv.unsettled:
		return nil
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
	if inv := b.invs[id]; inv != nil && inv.alias != "" && !inv.scopeSettled() {
		// Keep the exact operation identity after observation ended. Neither
		// an empty map nor a new incarnation can repair an unknown effect.
		inv.unsettled = true
		b.blocked[inv.alias] = true
		return
	}
	delete(b.invs, id)
}

var errEnded = errors.New("invocation ended")

func (b *Broker) send(m processshim.BrokerMessage) error {
	b.sendMu.Lock()
	defer b.sendMu.Unlock()
	return sandboxwire.WriteFrame(b.conn, processshim.Frame(m))
}
