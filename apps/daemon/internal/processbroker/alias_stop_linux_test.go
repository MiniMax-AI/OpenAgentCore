//go:build linux

package processbroker

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

func TestStopAliasesRequiresScopeClosed(t *testing.T) {
	for _, leaderExited := range []bool{false, true} {
		t.Run(map[bool]string{false: "leader running", true: "leader exited"}[leaderExited], func(t *testing.T) {
			p := newPeer()
			p.holdScope = true
			b, relay := newAliasBroker(t, p, 0)
			relay.send(aliasOpen(1, "a"))
			relay.await(t, relay.started, 1)
			if leaderExited {
				p.mu.Lock()
				status := sp.ExitStatus{Kind: sp.ExitCode}
				p.st.State, p.st.Exit, p.background = sp.StateExited, &status, true
				p.emit(func(h sp.EventHeader) sp.Event { return sp.ExitedEvent{EventHeader: h, Status: status} })
				p.mu.Unlock()
				relay.await(t, relay.exited, 1)
			}
			done := make(chan error, 1)
			go func() { done <- b.StopAliases(t.Context(), []string{"a"}) }()
			select {
			case <-p.cancelled:
			case <-time.After(5 * time.Second):
				t.Fatal("target scope received no Cancel")
			}
			select {
			case err := <-done:
				t.Fatalf("Cancel acceptance/leader exit settled a live scope: %v", err)
			default:
			}
			p.mu.Lock()
			p.closeScope()
			p.mu.Unlock()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("ScopeClosed did not settle target")
			}
		})
	}
}

func TestStopAliasesFencesUnpreparedOpenAndPreservesOthers(t *testing.T) {
	p := newPeer()
	b, relay := newAliasBroker(t, p, 0)
	// An Open is admitted, but its goroutine has not reached prepare or Start.
	b.mu.Lock()
	old := b.newInvocation(aliasOpen(1, "a"))
	old.alias = "a"
	b.invs[1], b.lastID = old, 1
	b.wg.Add(1)
	b.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- b.StopAliases(t.Context(), []string{"a"}) }()
	select {
	case <-old.gone.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("admitted Open was not selected")
	}
	relay.send(aliasOpen(2, "a"))
	relay.await(t, relay.ended, 2)
	relay.send(aliasOpen(3, "b"))
	relay.await(t, relay.started, 3)
	go old.serve()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unprepared target did not settle")
	}
	b.mu.Lock()
	blocked, other := b.blocked["a"], b.invs[3]
	b.mu.Unlock()
	if blocked || other == nil || other.lost() || p.counts().cancels != 0 {
		t.Fatal("target admission stayed closed or the unrelated service was cancelled")
	}
	if !old.lost() {
		t.Fatal("reopening the alias released the selected old invocation")
	}
}

func TestStopAliasesRetainsUnconfirmedOperation(t *testing.T) {
	b, relay := newAliasBroker(t, newPeer(), 0)
	inv := b.newInvocation(aliasOpen(1, "a"))
	inv.alias, inv.possibleEffect, inv.inst = "a", true, sandboxwire.NewID()
	b.mu.Lock()
	b.invs[1], b.lastID = inv, 1
	b.mu.Unlock()
	inv.teardown()
	if err := b.StopAliases(t.Context(), []string{"a"}); !errors.Is(err, ErrUnsettled) {
		t.Fatalf("unconfirmed Start was accepted as no effects: %v", err)
	}
	b.mu.Lock()
	retained := b.invs[1] == inv && inv.unsettled && b.blocked["a"]
	b.mu.Unlock()
	if !retained || inv.id.IsZero() || inv.inst.IsZero() {
		t.Fatal("unconfirmed operation identity was discarded")
	}
	relay.send(aliasOpen(2, "a"))
	relay.await(t, relay.ended, 1)
	relay.await(t, relay.ended, 2)
}

func TestStopAliasesInterruptedWaitKeepsOwnership(t *testing.T) {
	p := newPeer()
	p.holdScope = true
	b, relay := newAliasBroker(t, p, 0)
	relay.send(aliasOpen(1, "a"))
	relay.await(t, relay.started, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.StopAliases(ctx, []string{"a"}) }()
	select {
	case <-p.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("missing Cancel")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUnsettled) {
			t.Fatalf("wait outcome: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled wait did not return")
	}
	b.mu.Lock()
	retained := b.blocked["a"] && b.invs[1] != nil
	b.mu.Unlock()
	if !retained {
		t.Fatal("cancelled wait released ownership")
	}
	relay.send(aliasOpen(2, "a"))
	relay.await(t, relay.ended, 2)
	p.mu.Lock()
	p.closeScope()
	p.mu.Unlock()
	relay.await(t, relay.ended, 1)
}

func TestStopAliasesRejectsUnsupportedScopeAndUnknownTarget(t *testing.T) {
	b := &Broker{cfg: Config{Scope: sp.ScopePOSIXSession}}
	var failure *sp.Failure
	if err := b.StopAliases(t.Context(), []string{"a"}); !errors.As(err, &failure) || failure.Code != sp.CodeUnsupported {
		t.Fatalf("weak scope: %v", err)
	}
	b.cfg.Scope = sp.ScopeCgroupV2
	if err := b.StopAliases(t.Context(), []string{"unknown"}); !errors.As(err, &failure) || failure.Code != sp.CodeInvalidArgument {
		t.Fatalf("unknown target: %v", err)
	}
}

func TestStopAliasesJoinsRetriedStartingOperation(t *testing.T) {
	p := newPeer()
	p.startLate, p.holdScope = true, true
	b, relay := newAliasBroker(t, p, sp.OpStart)
	relay.send(aliasOpen(1, "a"))
	select {
	case <-p.attachedStarting:
	case <-time.After(5 * time.Second):
		t.Fatal("uncertain Start was not reattached")
	}
	done := make(chan error, 1)
	go func() { done <- b.StopAliases(t.Context(), []string{"a"}) }()
	select {
	case <-p.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("Starting operation was not cancelled")
	}
	b.mu.Lock()
	inv := b.invs[1]
	b.mu.Unlock()
	inv.shimGone()
	if p.counts().cancels != 1 {
		t.Fatal("shim loss resent the targeted cancellation")
	}
	select {
	case err := <-done:
		t.Fatalf("Starting operation settled: %v", err)
	default:
	}
	p.runStarting()
	relay.await(t, relay.started, 1)
	select {
	case err := <-done:
		t.Fatalf("late Started/Exited settled without ScopeClosed: %v", err)
	default:
	}
	p.mu.Lock()
	p.closeScope()
	p.mu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late scope did not settle")
	}
}

type aliasRelay struct {
	conn                   net.Conn
	mu                     sync.Mutex
	started, exited, ended chan uint64
}

func newAliasBroker(t *testing.T, p *peer, lose uint16) (*Broker, *aliasRelay) {
	t.Helper()
	p.strongScope = true
	sv, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	left, right := os.NewFile(uintptr(sv[0]), "broker"), os.NewFile(uintptr(sv[1]), "relay")
	defer left.Close()
	defer right.Close()
	c, err := net.FileConn(right)
	if err != nil {
		t.Fatal(err)
	}
	r := &aliasRelay{conn: c, started: make(chan uint64, 8), exited: make(chan uint64, 8), ended: make(chan uint64, 8)}
	b, err := Start(Config{Relay: left, Scope: sp.ScopeCgroupV2, Dial: p.dial(lose), Executables: Executables{Aliases: map[string]Command{
		"a": {Executable: "/bin/server", Dir: "/"}, "b": {Executable: "/bin/other", Dir: "/"},
	}}})
	if err != nil {
		c.Close()
		t.Fatal(err)
	}
	go func() {
		for {
			f, err := sandboxwire.ReadFrame(c, processshim.MaxFrameBytes)
			if err != nil {
				return
			}
			m, err := processshim.DecodeBroker(f)
			if err != nil {
				return
			}
			switch m := m.(type) {
			case processshim.Started:
				r.started <- m.ID
			case processshim.Exit:
				r.exited <- m.ID
			case processshim.End:
				r.ended <- m.ID
			case processshim.Output:
				r.send(processshim.Written{ID: m.ID, FD: m.FD, Seq: m.Seq})
			case processshim.Close:
				r.send(processshim.Written{ID: m.ID, FD: m.FD, Seq: m.Seq})
			}
		}
	}()
	t.Cleanup(func() { b.Close(); c.Close() })
	return b, r
}

func aliasOpen(id uint64, alias string) processshim.Open {
	return processshim.Open{ID: id, Request: processshim.Request{Version: processshim.Version, ExecPath: []byte("/.oac/bin/" + alias), Argv: [][]byte{[]byte(alias)}, Cwd: []byte("/")}}
}

func (r *aliasRelay) send(m processshim.RelayMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = sandboxwire.WriteFrame(r.conn, processshim.Frame(m))
}

func (r *aliasRelay) await(t *testing.T, events <-chan uint64, want uint64) {
	t.Helper()
	select {
	case id := <-events:
		if id != want {
			t.Fatalf("invocation %d, want %d", id, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing relay event")
	}
}
