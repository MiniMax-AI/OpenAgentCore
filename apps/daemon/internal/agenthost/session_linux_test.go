//go:build linux

package agenthost

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync"
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

func TestSweepEndsSessionProcessesBeforeRemovingDirectories(t *testing.T) {
	cfg := Config{StateDir: t.TempDir(), UIDs: UIDRange{First: 70000, Count: 8}}
	if err := sweep(cfg, &fakeProcesses{}, time.Second); err != nil {
		t.Fatalf("Sweep without sessions: %v", err)
	}
	leave := func() {
		left := filepath.Join(sessionsDir(cfg.StateDir), "left", "home")
		if err := os.MkdirAll(left, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	leave()
	// Any of the four uids of any thread places a process in the range.
	outside := [4]uint32{1000, 1000, 1000, 1000}
	procs := &fakeProcesses{list: []task{{tgid: 10, tid: 10, uids: outside}, {tgid: 10, tid: 12, uids: [4]uint32{1000, 1000, 1000, 70003}}, {tgid: 11, tid: 11, uids: outside}}}
	if err := sweep(cfg, procs, time.Second); err != nil || !reflect.DeepEqual(procs.ended, []int{10}) || len(leftSessions(t, cfg)) != 0 {
		t.Fatalf("Sweep = %v, ended %v, %d Session directories left", err, procs.ended, len(leftSessions(t, cfg)))
	}
	leave()
	procs = &fakeProcesses{list: []task{{tgid: 12, tid: 12, uids: [4]uint32{70000, 70000, 70000, 70000}}}, stubborn: true}
	if err := sweep(cfg, procs, 100*time.Millisecond); !errors.Is(err, ErrTeardown) || len(leftSessions(t, cfg)) != 1 {
		t.Fatalf("Sweep with a process that outlives the bound = %v, %d Session directories left", err, len(leftSessions(t, cfg)))
	}
}

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
		p, err := s.own(lv, v, fakeWorld{stop: stop}, func() {}, clirunner.StartOptions{Parent: context.Background(), KillTimeout: time.Second}, ends)
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

// newOwnerSession is a Session with no directory, uid or link.
func newOwnerSession(t *testing.T) *session {
	s := &session{log: slog.New(slog.DiscardHandler)}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	t.Cleanup(s.cancel)
	s.link = newLinkOwner(nil, Binding{AttachmentID: sandboxwire.NewID()}, s.fail)
	return s
}

// fakeView is a view that ends when closed.
type fakeView struct {
	exit chan struct{}
	once sync.Once
}

func (v *fakeView) Signal(syscall.Signal) error { return nil }

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
