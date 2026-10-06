//go:build linux

package agenthost

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/sessionview"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

func TestAllocationSkipsUIDsThatProcessesHold(t *testing.T) {
	r := UIDRange{First: 71000, Count: 2}
	// A thread holds the uid; its process's leader does not.
	procs := &fakeProcesses{list: []task{{tgid: 10, tid: 10, uids: [4]uint32{1000, 1000, 1000, 1000}}, {tgid: 10, tid: 11, uids: [4]uint32{1000, 71000, 1000, 1000}}}}
	id, err := allocUID(r, procs)
	if err != nil || id != 71001 {
		t.Fatalf("allocUID = %d, %v; want 71001", id, err)
	}
	defer freeUID(id)
	if _, err := allocUID(r, procs); !errors.Is(err, ErrCapacity) {
		t.Fatalf("allocUID with every uid taken = %v", err)
	}
	// The host's table shows this process with its own uids.
	self := uint32(os.Getuid())
	if held, err := heldUIDs(procfs{}, UIDRange{First: self, Count: 1}); err != nil || !held[self] {
		t.Fatalf("/proc shows uid %d held: %v, %v", self, held[self], err)
	}
}

func TestViewEndReleasesTheSlotBeforeTheProcessEnds(t *testing.T) {
	errDetach := errors.New("detach failed")
	for name, stop := range map[string]error{"clean world": nil, "failed detach": errDetach} {
		s := newOwnerSession(t)
		lv := &liveView{}
		s.live = lv
		s.views.Add(1)
		ends, err := newStdio(false)
		if err != nil {
			t.Fatal(err)
		}
		ends.closeChild()
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
		failed := s.ctx.Err() != nil
		err = s.finish(nil, nil, nil)
		switch {
		case stop == nil && (err != nil || failed):
			t.Errorf("%s: Run = %v, Session failed %v", name, err, failed)
		case stop != nil && (!errors.Is(err, ErrWorld) || !errors.Is(err, errDetach) || !failed):
			t.Errorf("%s: Run = %v, Session failed %v; want ErrWorld with the detach error", name, err, failed)
		}
	}
}

func TestFailureDuringTeardownCounts(t *testing.T) {
	s := newOwnerSession(t)
	// The relay revokes the attachment while Executor.Close waits.
	revoke := func() { s.link.closed(sandboxlink.AttachmentClosed{AttachmentID: s.link.binding.AttachmentID}) }
	if err := s.finish(closingExecutor(revoke), nil, nil); !errors.Is(err, ErrLink) {
		t.Fatalf("Run = %v, want the revocation", err)
	}
	// Once the link owner closes the attachment, the Link's reports explain nothing.
	s = newOwnerSession(t)
	if err := s.finish(nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	s.link.closed(sandboxlink.AttachmentClosed{AttachmentID: s.link.binding.AttachmentID})
	if s.ctx.Err() == nil {
		t.Fatal("teardown left the Session's context live")
	}
	s.failMu.Lock()
	defer s.failMu.Unlock()
	if s.failure != nil {
		t.Fatalf("a report after close failed the Session: %v", s.failure)
	}
}

func TestTurnDrainsOutputFromStart(t *testing.T) {
	s := newOwnerSession(t)
	output := make(chan proto.Envelope, 2*turnBuffer)
	s.in.Output = output
	// The adapter emits more than out holds before StartTurn returns.
	turn := &fakeTurn{}
	exec := &fakeExecutor{start: func(runID string, out chan<- proto.Envelope) (agent.Turn, error) {
		for range turnBuffer + 1 {
			out <- proto.Envelope{Type: proto.TypeOutputMessage, ID: runID}
		}
		out <- doneEnvelope(runID)
		close(out)
		return turn, nil
	}}
	if err := within(t, func() error { return s.turn(exec, Input{RunID: "r"}) }); err != nil {
		t.Fatalf("turn = %v", err)
	}
	if got := drainAll(output); len(got) != turnBuffer+2 || got[len(got)-1].Type != proto.TypeDone || turn.cancelled.Load() || exec.closes != 0 {
		t.Fatalf("Output got %d envelopes; Turn cancelled %v; Executor closed %d times", len(got), turn.cancelled.Load(), exec.closes)
	}
	// A nil Turn leaves out with its caller, which closes it.
	var kept chan<- proto.Envelope
	exec = &fakeExecutor{start: func(_ string, out chan<- proto.Envelope) (agent.Turn, error) {
		kept = out
		return nil, errors.New("refused")
	}}
	if err := within(t, func() error { return s.turn(exec, Input{RunID: "r"}) }); !errors.Is(err, ErrTurn) || !isClosed(kept) {
		t.Fatalf("turn without a Turn = %v; out closed %v", err, isClosed(kept))
	}
}

func TestTurnPublishesDoneAfterSettlementAndClose(t *testing.T) {
	s := newOwnerSession(t)
	output := make(chan proto.Envelope, 4)
	s.in.Output = output
	published := -1
	exec := &fakeExecutor{
		start: func(runID string, out chan<- proto.Envelope) (agent.Turn, error) {
			out <- doneEnvelope(runID)
			close(out)
			return &fakeTurn{settleErr: errors.New("settlement lost")}, nil
		},
		close: func() error {
			published = len(output)
			return nil
		},
	}
	err := within(t, func() error { return s.turn(exec, Input{RunID: "r"}) })
	got := drainAll(output)
	if !errors.Is(err, ErrTurn) || published != 0 || len(got) != 2 || got[0].Type != proto.TypeError || got[1].Type != proto.TypeDone {
		t.Fatalf("turn = %v; %d envelopes published before Close; Output got %v, want Error then Done", err, published, got)
	}
	// A Turn that failed to start delivers its Done during Close.
	s = newOwnerSession(t)
	s.in.Output = output
	native, err := proto.NewEnvelope(proto.TypeDone, "r", proto.DonePayload{Content: "native"})
	if err != nil {
		t.Fatal(err)
	}
	var kept chan<- proto.Envelope
	exec = &fakeExecutor{
		start: func(_ string, out chan<- proto.Envelope) (agent.Turn, error) {
			kept = out
			return &fakeTurn{settleErr: errors.New("settlement lost")}, errors.New("start failed")
		},
		close: func() error {
			kept <- native
			close(kept)
			return nil
		},
	}
	err = within(t, func() error { return s.turn(exec, Input{RunID: "r"}) })
	if got := drainAll(output); !errors.Is(err, ErrTurn) || len(got) != 2 || got[0].Type != proto.TypeError || !reflect.DeepEqual(got[1], native) {
		t.Fatalf("turn = %v; Output got %v, want Error then the Done from Close", err, got)
	}
}

func TestFailedCloseKeepsTheSessionDirectoryAndUID(t *testing.T) {
	errStuck := errors.New("close stuck")
	for name, recovers := range map[string]bool{"Close fails until the view ends": true, "Close keeps failing": false} {
		s := newOwnerSession(t)
		output := make(chan proto.Envelope, 4)
		s.in.Output = output
		// Each case takes its own uid.
		uid, err := allocUID(UIDRange{First: 72000, Count: 2}, &fakeProcesses{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { freeUID(uid) })
		s.uid = uid
		if s.dir, err = createSessionDir(t.TempDir(), sandboxwire.NewID(), uid); err != nil {
			t.Fatal(err)
		}
		v := &fakeView{exit: make(chan struct{})}
		s.live = &liveView{view: v}
		s.views.Add(1)
		go func() {
			v.Wait()
			s.views.Done()
		}()
		// The Turn's settlement fails, and its adapter never closes out.
		var kept chan<- proto.Envelope
		exec := &fakeExecutor{
			start: func(runID string, out chan<- proto.Envelope) (agent.Turn, error) {
				kept = out
				out <- proto.Envelope{Type: proto.TypeOutputMessage, ID: runID}
				out <- doneEnvelope(runID)
				return &fakeTurn{settleErr: errors.New("settlement lost")}, nil
			},
			close: func() error {
				select {
				case <-v.exit:
					if recovers {
						return nil
					}
				default:
				}
				return errStuck
			},
		}
		err = within(t, func() error { return s.finish(exec, s.turn(exec, Input{RunID: "r"}), nil) })
		_, statErr := os.Stat(string(s.dir))
		uids.Lock()
		used := uids.used[uid]
		uids.Unlock()
		switch {
		case !errors.Is(err, ErrTurn) || exec.closes != 2 || len(output) != 1:
			t.Errorf("%s: Run = %v; Executor closed %d times; Output got %d envelopes, want only the output message", name, err, exec.closes, len(output))
		case recovers && (errors.Is(err, ErrTeardown) || statErr == nil || used):
			t.Errorf("%s: Run = %v; directory kept %v; uid in use %v", name, err, statErr == nil, used)
		case !recovers && (!errors.Is(err, ErrTeardown) || !errors.Is(err, errStuck) || statErr != nil || !used):
			t.Errorf("%s: Run = %v; directory kept %v; uid in use %v; want ErrTeardown keeping both", name, err, statErr == nil, used)
		}
		// The forwarder has returned, so nothing reaches Output any more.
		select {
		case <-s.fwd.done:
		default:
			t.Errorf("%s: the forwarder outlives Run", name)
		}
		kept <- proto.Envelope{Type: proto.TypeOutputMessage, ID: "r"}
	}
}

// newOwnerSession is a Session with no directory, uid or link.
func newOwnerSession(t *testing.T) *session {
	s := &session{log: slog.New(slog.DiscardHandler)}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	t.Cleanup(s.cancel)
	context.AfterFunc(s.ctx, s.closeLive)
	s.link = newLinkOwner(nil, Binding{AttachmentID: sandboxwire.NewID()}, s.fail)
	return s
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

// drainAll returns what ch holds.
func drainAll(ch chan proto.Envelope) []proto.Envelope {
	var list []proto.Envelope
	for len(ch) > 0 {
		list = append(list, <-ch)
	}
	return list
}

// isClosed reports whether ch is closed.
func isClosed(ch chan<- proto.Envelope) (closed bool) {
	defer func() { closed = recover() != nil }()
	select {
	case ch <- proto.Envelope{}:
	default:
	}
	return false
}

func doneEnvelope(runID string) proto.Envelope {
	e, err := proto.NewEnvelope(proto.TypeDone, runID, proto.DonePayload{})
	if err != nil {
		panic(err)
	}
	return e
}

// fakeExecutor starts each Turn with start; Close returns close's result.
type fakeExecutor struct {
	start  func(runID string, out chan<- proto.Envelope) (agent.Turn, error)
	close  func() error
	closes int
}

func (e *fakeExecutor) StartTurn(_ context.Context, runID string, _ proto.MessageInput, out chan<- proto.Envelope) (agent.Turn, error) {
	return e.start(runID, out)
}

func (e *fakeExecutor) Close(context.Context) error {
	e.closes++
	if e.close == nil {
		return nil
	}
	return e.close()
}

// fakeTurn settles at once: reusable, or with settleErr.
type fakeTurn struct {
	settleErr error
	cancelled atomic.Bool
}

func (t *fakeTurn) Cancel(context.Context) error {
	t.cancelled.Store(true)
	return nil
}

func (t *fakeTurn) CancellationOutcome() proto.DonePayload { return proto.DonePayload{} }

func (t *fakeTurn) SteerWithReceipt(context.Context, proto.PromptSteerPayload, func()) error {
	return agent.ErrUnsupportedOperation
}

func (t *fakeTurn) AwaitSettlement(context.Context) (agent.TurnSettlement, error) {
	return agent.TurnSettlement{Reusable: t.settleErr == nil}, t.settleErr
}

// fakeView is a view that ends when closed.
type fakeView struct {
	exit chan struct{}
	once sync.Once
}

func (v *fakeView) Signal(syscall.Signal) error { return nil }

func (v *fakeView) Relay() *os.File { return nil }

func (v *fakeView) Spawn(context.Context, string, []string, []string, string, [3]*os.File) (*sessionview.Spawned, error) {
	return nil, sessionview.ErrClosed
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

// closingExecutor runs itself when closed.
type closingExecutor func()

func (closingExecutor) StartTurn(context.Context, string, proto.MessageInput, chan<- proto.Envelope) (agent.Turn, error) {
	return nil, errors.New("no Turns")
}

func (e closingExecutor) Close(context.Context) error {
	e()
	return nil
}
