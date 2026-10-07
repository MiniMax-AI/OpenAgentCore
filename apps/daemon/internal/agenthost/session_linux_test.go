//go:build linux

package agenthost

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/gateway"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/sandboxlinktest"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

func TestAllocationSkipsUIDsThatProcessesHold(t *testing.T) {
	r := UIDRange{First: 71000, Count: 2}
	// A thread holds the uid; its process's leader does not.
	tasks := func() ([][4]uint32, error) {
		return [][4]uint32{{1000, 1000, 1000, 1000}, {1000, 71000, 1000, 1000}}, nil
	}
	id, err := allocUID(r, tasks)
	if err != nil || id != 71001 {
		t.Fatalf("allocUID = %d, %v; want 71001", id, err)
	}
	defer freeUID(id)
	if _, err := allocUID(r, tasks); !errors.Is(err, ErrCapacity) {
		t.Fatalf("allocUID with every uid taken = %v", err)
	}
	// The host's table shows this process with its own uids.
	self := uint32(os.Getuid())
	if held, err := heldUIDs(taskUIDs, UIDRange{First: self, Count: 1}); err != nil || !held[self] {
		t.Fatalf("/proc shows uid %d held: %v, %v", self, held[self], err)
	}
}

func TestViewEndReleasesTheSlotBeforeTheProcessEnds(t *testing.T) {
	errDetach := errors.New("detach failed")
	for name, stop := range map[string]error{"clean world": nil, "failed detach": errDetach} {
		s, logged := newOwnerSession(t)
		lv := &liveView{}
		s.live = lv
		s.views.Add(1)
		child, ends, err := sessionview.Stdio([3]*os.File{}, false, uint32(os.Getuid()), uint32(os.Getgid()))
		if err != nil {
			t.Fatal(err)
		}
		closeFiles(child[:])
		v := &fakeView{exit: make(chan struct{})}
		p, err := s.own(lv, v, fakeWorld{stop: stop}, func() {}, clirunner.StartOptions{Parent: context.Background(), KillTimeout: time.Second}, ends, 0)
		if err != nil {
			t.Fatal(err)
		}
		v.Close()
		_ = p.Wait()
		s.mu.Lock()
		live := s.live
		s.mu.Unlock()
		if live != nil {
			t.Errorf("%s: the view slot is taken after Process.Wait", name)
		}
		failed, err := s.ctx.Err() != nil, logged.take()
		switch {
		case stop == nil && (err != nil || failed):
			t.Errorf("%s: logged %v, Session failed %v", name, err, failed)
		case stop != nil && (!errors.Is(err, ErrWorld) || !errors.Is(err, errDetach) || !failed):
			t.Errorf("%s: logged %v, Session failed %v; want ErrWorld with the detach error", name, err, failed)
		}
	}
}

// TestViewEndStopsTheGateway checks that the gateway's listeners are closed
// once Process.Wait returns. It serves the gateway in the test's own network
// namespace, so it runs with the view suite.
func TestViewEndStopsTheGateway(t *testing.T) {
	if os.Getenv(gateEnv) != "1" {
		t.Skipf("set %s=1 and run the test binary as root in a privileged container; see view_linux_test.go", gateEnv)
	}
	// The calling thread's namespace: the main thread stays wherever a locked
	// goroutine that exited on it left it, such as a view's network namespace.
	ns, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	defer ns.Close()
	cfg := gateway.Config{Model: modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: "https://model.test", APIKey: upstreamKey}}
	eps, err := gateway.Plan(cfg)
	if err != nil {
		t.Fatal(err)
	}
	stop, err := gateway.Start(ns, cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := newOwnerSession(t)
	lv := &liveView{}
	s.live = lv
	s.views.Add(1)
	child, ends, err := sessionview.Stdio([3]*os.File{}, false, uint32(os.Getuid()), uint32(os.Getgid()))
	if err != nil {
		t.Fatal(err)
	}
	closeFiles(child[:])
	v := &fakeView{exit: make(chan struct{})}
	p, err := s.own(lv, v, fakeWorld{}, stop, clirunner.StartOptions{Parent: context.Background(), KillTimeout: time.Second}, ends, 0)
	if err != nil {
		t.Fatal(err)
	}
	model := strings.TrimPrefix(eps.Model, "http://")
	c, err := net.Dial("tcp", model)
	if err != nil {
		t.Fatalf("the gateway does not serve: %v", err)
	}
	c.Close()
	v.Close()
	_ = p.Wait()
	if c, err := net.Dial("tcp", model); err == nil {
		c.Close()
		t.Fatal("the model listener accepts after Process.Wait")
	}
}

// TestSpawnKeepsItsErrors checks that only a view that has ended makes a Spawn fail with ErrNoLiveView, and that every other failure keeps its own error.
func TestSpawnKeepsItsErrors(t *testing.T) {
	emfile := &sessionview.Error{Kind: sessionview.ErrLauncher, Op: "pipe", Err: syscall.EMFILE}
	for _, c := range []struct {
		err   error
		ended bool
	}{{sessionview.ErrExited, true}, {sessionview.ErrClosed, true}, {emfile, false}, {sessionview.ErrExec, false}, {context.Canceled, false}} {
		s, _ := newOwnerSession(t)
		s.plan = &plan{view: agent.View{LocalExec: []string{"/bin/true"}}}
		s.live = &liveView{view: &fakeView{exit: make(chan struct{}), spawnErr: c.err}}
		_, err := s.spawn(clirunner.StartOptions{Binary: "/bin/true", Dir: "/", OwnProcessGroup: true})
		if !errors.Is(err, c.err) || errors.Is(err, agent.ErrNoLiveView) != c.ended {
			t.Errorf("Spawn failing with %v = %v; want that error, and ErrNoLiveView only for an ended view", c.err, err)
		}
	}
}

// TestFailedCloseKeepsTheTransientEntriesAndUID checks that Close retries a
// view Executor whose Close fails once the views have ended, keeps the
// transient entries and the uid while it still fails, and that a later Close
// releases them. The home stays.
func TestFailedCloseKeepsTheTransientEntriesAndUID(t *testing.T) {
	errStuck := errors.New("close stuck")
	for name, recovers := range map[string]bool{"Close fails until the view ends": true, "Close keeps failing": false} {
		s, _ := newOwnerSession(t)
		// Each case takes its own uid.
		uid, err := allocUID(UIDRange{First: 72000, Count: 2}, noTasks)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { freeUID(uid) })
		s.uid = uid
		if s.dir, err = openSessionDir(t.TempDir(), sandboxwire.NewID(), uid); err != nil {
			t.Fatal(err)
		}
		v := &fakeView{exit: make(chan struct{})}
		s.live = &liveView{view: v}
		s.views.Add(1)
		go func() {
			v.Wait()
			s.views.Done()
		}()
		stuck := !recovers
		exec := &fakeExecutor{close: func() error {
			select {
			case <-v.exit:
				if !stuck {
					return nil
				}
			default:
			}
			return errStuck
		}}
		s.exec = exec
		check := func(when string, err error, closes int, released bool) {
			t.Helper()
			_, entriesErr := os.Stat(s.dir.entry(etcEntry))
			_, homeErr := os.Stat(s.dir.entry(homeEntry))
			owned.Lock()
			used := owned.uids[uid]
			owned.Unlock()
			if exec.closes != closes || homeErr != nil || released == (entriesErr == nil) || released == used ||
				!released && (!errors.Is(err, ErrTeardown) || !errors.Is(err, errStuck)) || released && err != nil {
				t.Errorf("%s, %s: Close = %v after %d view Executor Closes; home %v, transient entries kept %v, uid in use %v",
					name, when, err, exec.closes, homeErr, entriesErr == nil, used)
			}
		}
		err = within(t, func() error { return s.Close(context.Background()) })
		check("first Close", err, 2, recovers)
		if !recovers {
			stuck = false
			err = within(t, func() error { return s.Close(context.Background()) })
			check("later Close", err, 3, true)
		}
	}
}

// TestUnconfirmedAttachmentCloseKeepsTheUID checks that every Close fails
// while the relay has not confirmed the attachment's close, keeping the
// transient entries, the uid and the Session's claim, and that a Close once
// the Link recovers releases them.
func TestUnconfirmedAttachmentCloseKeepsTheUID(t *testing.T) {
	auth := sandboxlinktest.NewAuthority()
	rl := relay.New(auth)
	srv := httptest.NewServer(rl)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { rl.Close() })
	cfg := Config{RelayURL: "ws://" + strings.TrimPrefix(srv.URL, "http://"), RuntimeID: sandboxwire.NewID(), Credential: []byte("runtime-credential")}
	auth.AddRuntime(cfg.Credential, cfg.RuntimeID)
	var down atomic.Bool
	down.Store(true)
	dial := func(ctx context.Context, onClosed func(sandboxlink.AttachmentClosed)) (*sandboxlink.AttachLink, error) {
		if down.Load() {
			// Not retryable, so the close gives up at once, as at its bound.
			return nil, sandboxlink.Fail(sandboxlink.PermissionDenied)
		}
		return relayDial(cfg)(ctx, onClosed)
	}
	s, _ := newOwnerSession(t)
	s.id = sandboxwire.NewID()
	s.link = newLinkOwner(dial, newBinding(newResource()), sandboxwire.NewID(), s.fail)
	s.link.opened = true // an Open was sent, so the attachment may exist
	uid, err := allocUID(UIDRange{First: 72100, Count: 1}, noTasks)
	if err != nil || !claimSession(s.id) {
		t.Fatalf("allocUID = %v, or the Session is claimed", err)
	}
	t.Cleanup(func() {
		freeUID(uid)
		releaseSession(s.id)
	})
	s.uid = uid
	if s.dir, err = openSessionDir(t.TempDir(), s.id, uid); err != nil {
		t.Fatal(err)
	}
	check := func(when string, err error, released bool) {
		t.Helper()
		_, entriesErr := os.Stat(s.dir.entry(etcEntry))
		owned.Lock()
		used, claimed := owned.uids[uid], owned.sessions[s.id]
		owned.Unlock()
		if released != (err == nil) || !released && !errors.Is(err, ErrTeardown) || released == (entriesErr == nil) || released == used || released == claimed {
			t.Errorf("%s: Close = %v; transient entries kept %v, uid in use %v, Session claimed %v", when, err, entriesErr == nil, used, claimed)
		}
	}
	// A retry while the Link is still down tries the attachment's close again.
	for _, when := range []string{"while the Link is down", "a retry while the Link is down"} {
		check(when, within(t, func() error { return s.Close(context.Background()) }), false)
	}
	down.Store(false)
	check("once the Link recovers", within(t, func() error { return s.Close(context.Background()) }), true)
}

// newOwnerSession is a Session with no directory, uid or link, and what it
// logs.
func newOwnerSession(t *testing.T) (*session, *failures) {
	logged := &failures{}
	s := &session{log: slog.New(logged)}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	t.Cleanup(s.cancel)
	context.AfterFunc(s.ctx, s.closeLive)
	s.link = newLinkOwner(nil, Binding{}, sandboxwire.NewID(), s.fail)
	return s, logged
}

// within runs f and fails t unless f returns within a bound.
func within(t *testing.T, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("blocked")
		return nil
	}
}

// fakeExecutor is a view Executor whose Close returns close's result.
type fakeExecutor struct {
	close  func() error
	closes int
}

func (e *fakeExecutor) StartTurn(context.Context, string, proto.MessageInput, chan<- proto.Envelope) (agent.Turn, error) {
	return nil, errors.New("no Turns")
}

func (e *fakeExecutor) Close(context.Context) error {
	e.closes++
	return e.close()
}

// fakeView is a view that ends when closed and whose spawns fail with spawnErr.
type fakeView struct {
	exit     chan struct{}
	once     sync.Once
	spawnErr error
}

func (v *fakeView) Signal(syscall.Signal) error { return nil }

func (v *fakeView) Relay() *os.File { return nil }

func (v *fakeView) RelayLost() <-chan struct{} { return nil }

func (v *fakeView) Spawn(context.Context, string, []string, []string, string, bool) (*sessionview.Spawned, error) {
	return nil, v.spawnErr
}

func (v *fakeView) Wait() (sessionview.Exit, error) {
	<-v.exit
	return sessionview.Exit{}, nil
}

func (v *fakeView) Close() error {
	v.once.Do(func() { close(v.exit) })
	return nil
}

// fakeWorld is a world that is never lost and whose Stop returns stop.
type fakeWorld struct{ stop error }

func (w fakeWorld) Stop() error         { return w.stop }
func (fakeWorld) Lost() <-chan struct{} { return nil }
func (fakeWorld) Err() error            { return nil }
