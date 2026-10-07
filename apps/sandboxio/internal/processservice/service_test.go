//go:build linux

package processservice

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// TestMain runs like the service binary: the trampoline hook first, then a
// child subreaper whose one reap loop owns every child exit.
func TestMain(m *testing.M) {
	Init()
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		panic(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go Reap(ctx)
	code := m.Run()
	cancel()
	os.Exit(code)
}

const testPath = "/usr/bin:/bin"

type harness struct {
	t   *testing.T
	svc *Service
	att sandboxwire.ID
}

func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	svc, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, svc: svc, att: sandboxwire.NewID()}
}

// connect opens a stream for the harness attachment over net.Pipe.
func (h *harness) connect() *sp.Client {
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		sp.Serve(context.Background(), server, sp.Attachment{ID: h.att}, h.svc)
	}()
	c := sp.NewClient(client)
	h.t.Cleanup(func() {
		c.Close()
		<-done
	})
	return c
}

func (h *harness) start(c *sp.Client, spec sp.ProcessSpec) *sp.Operation {
	h.t.Helper()
	op, disp, err := c.Start(context.Background(), h.svc.instance, sandboxwire.NewID(), spec)
	if err != nil || disp != sp.StartCreated {
		h.t.Fatalf("start: %v %v", disp, err)
	}
	return op
}

func pipeSpec(argv ...string) sp.ProcessSpec {
	spec := sp.ProcessSpec{Executable: []byte(argv[0]), Env: []sp.EnvVar{{Name: []byte("PATH"), Value: []byte(testPath)}}, Cwd: []byte("/"), Umask: 0o022, IOMode: sp.IOPipes, Scope: sp.ScopePOSIXSession}
	for _, a := range argv {
		spec.Argv = append(spec.Argv, []byte(a))
	}
	return spec
}

func ptySpec(argv ...string) sp.ProcessSpec {
	spec := pipeSpec(argv...)
	spec.IOMode = sp.IOPTY
	spec.PTY = &sp.PTYSpec{Size: sp.WindowSize{Rows: 24, Cols: 80}, Term: []byte("xterm")}
	return spec
}

func next(t *testing.T, op *sp.Operation) sp.Event {
	t.Helper()
	select {
	case ev, ok := <-op.Events():
		if !ok {
			t.Fatal("events closed")
		}
		return ev
	case <-time.After(10 * time.Second):
		t.Fatal("no event")
	}
	return nil
}

// events collects events until one of type until arrives.
func events(t *testing.T, op *sp.Operation, until uint16) []sp.Event {
	t.Helper()
	var got []sp.Event
	for len(got) == 0 || got[len(got)-1].MessageType() != until {
		got = append(got, next(t, op))
	}
	return got
}

func output(evs []sp.Event, stream sp.Stream) string {
	var b strings.Builder
	for _, ev := range evs {
		if o, ok := ev.(sp.OutputEvent); ok && o.Stream == stream {
			b.Write(o.Data)
		}
	}
	return b.String()
}

func find[E sp.Event](t *testing.T, evs []sp.Event) (E, int) {
	t.Helper()
	for i, ev := range evs {
		if e, ok := ev.(E); ok {
			return e, i
		}
	}
	var zero E
	t.Fatalf("no %T in %v", zero, evs)
	return zero, -1
}

func wantCode(t *testing.T, err error, code sp.ErrorCode) {
	t.Helper()
	var f *sp.Failure
	if !errors.As(err, &f) || f.Code != code {
		t.Fatalf("got %v, want %v", err, code)
	}
}

// leader returns the operation's session ID, its leader's PID.
func (h *harness) leader(op *sp.Operation) int {
	h.svc.mu.Lock()
	defer h.svc.mu.Unlock()
	return h.svc.ops[opKey{h.att, op.Ref().OperationID}].sid()
}

// waitReaped waits until the reaper has reaped the operation's leader.
func (h *harness) waitReaped(op *sp.Operation) {
	h.t.Helper()
	h.svc.mu.Lock()
	o := h.svc.ops[opKey{h.att, op.Ref().OperationID}]
	h.svc.mu.Unlock()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		o.mu.Lock()
		gone := o.leaderGone
		o.mu.Unlock()
		if gone {
			return
		}
	}
	h.t.Fatal("the leader was never reaped")
}

// waitMembers waits until the operation's session has n live processes named
// comm.
func (h *harness) waitMembers(op *sp.Operation, comm string, n int) {
	h.t.Helper()
	sid := h.leader(op)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		entries, _ := os.ReadDir("/proc")
		count := 0
		for _, e := range entries {
			pid, err := strconv.Atoi(e.Name())
			if err != nil {
				continue
			}
			st, err := readStat(pid)
			if err != nil || st.session != sid || !st.live() {
				continue
			}
			if b, err := os.ReadFile("/proc/" + e.Name() + "/comm"); err == nil && strings.TrimSpace(string(b)) == comm {
				count++
			}
		}
		if count >= n {
			return
		}
	}
	h.t.Fatalf("session %d never had %d %s processes", sid, n, comm)
}

func TestEnvironmentIsExplicit(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	spec := pipeSpec("env")
	spec.Env = append(spec.Env, sp.EnvVar{Name: []byte("GREETING"), Value: []byte("hello world")})
	evs := events(t, h.start(h.connect(), spec), sp.EventOutputClosed)
	if got, want := output(evs, sp.StreamStdout), "PATH="+testPath+"\nGREETING=hello world\n"; got != want {
		t.Fatalf("environment %q, want %q", got, want)
	}
	if _, i := find[sp.StartedEvent](t, evs); i != 0 {
		t.Fatalf("first event %v", evs[0])
	}
}

func TestCwdUmaskArgv0(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	dir := t.TempDir()
	spec := pipeSpec("sh", "-c", `umask; pwd; echo "$0"`)
	spec.Argv[0] = []byte("renamed")
	spec.Cwd, spec.Umask = []byte(dir), 0o027
	evs := events(t, h.start(h.connect(), spec), sp.EventOutputClosed)
	if got, want := output(evs, sp.StreamStdout), "0027\n"+dir+"\nrenamed\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

func TestExitStatus(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	c := h.connect()
	exited, _ := find[sp.ExitedEvent](t, events(t, h.start(c, pipeSpec("sh", "-c", "exit 7")), sp.EventExited))
	if exited.Status != (sp.ExitStatus{Kind: sp.ExitCode, Code: 7}) {
		t.Fatalf("exit %+v", exited.Status)
	}
	exited, _ = find[sp.ExitedEvent](t, events(t, h.start(c, pipeSpec("sh", "-c", "kill -KILL $$")), sp.EventExited))
	if exited.Status != (sp.ExitStatus{Kind: sp.ExitSignal, Signal: 9}) {
		t.Fatalf("exit %+v", exited.Status)
	}
}

func TestStartFailure(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	evs := events(t, h.start(h.connect(), pipeSpec("no-such-command")), sp.EventStartFailed)
	if f := evs[0].(sp.StartFailedEvent).Failure; f.Code != sp.CodeNotFound || f.Effect != sandboxwire.EffectNone {
		t.Fatalf("failure %+v", f)
	}
}

// Exited follows every byte the leader left buffered: the readers wait until
// the leader is reaped, so all its output is still in the pipes then.
func TestOutputPrecedesExited(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	hold := make(chan struct{})
	h.svc.beforeRead = func() { <-hold }
	op := h.start(h.connect(), pipeSpec("sh", "-c", "printf out; printf err >&2"))
	h.waitReaped(op)
	close(hold)
	evs := events(t, op, sp.EventExited)
	if output(evs, sp.StreamStdout) != "out" || output(evs, sp.StreamStderr) != "err" {
		t.Fatalf("events %v", evs)
	}
}

// Output a background process writes after the leader exits follows Exited,
// and keeps OutputClosed pending until it ends.
func TestLaterOutputFollowsExited(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	op := h.start(h.connect(), pipeSpec("sh", "-c", `printf a; (read x <"$0"; printf b) & exit 0`, fifo))
	before := events(t, op, sp.EventExited)
	if err := os.WriteFile(fifo, []byte("\n"), 0); err != nil {
		t.Fatal(err)
	}
	after := events(t, op, sp.EventOutputClosed)
	if output(before, sp.StreamStdout) != "a" || output(after, sp.StreamStdout) != "b" || after[len(after)-1].(sp.OutputClosedEvent).Disposition != sp.OutputDrained {
		t.Fatalf("events %v then %v", before, after)
	}
}

// A pipe in packet mode keeps every packet whole at the replay limit, and
// Exited does not wait for the acknowledgement that lets the rest be read.
func TestPacketsAtReplayLimit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxReplayBytesPerOperation = sandboxwire.MaxChunk + 1000 // not a whole number of packets
	h := newHarness(t, cfg)
	const packet, packets = 4096, 20
	op := h.start(h.connect(), pipeSpec("dd", "if=/dev/zero", "bs="+strconv.Itoa(packet), "count="+strconv.Itoa(packets), "oflag=direct", "status=none"))
	evs := events(t, op, sp.EventExited)
	before := len(output(evs, sp.StreamStdout))
	if err := op.Ack(context.Background(), evs[len(evs)-1].Header().Sequence); err != nil {
		t.Fatal(err)
	}
	evs = events(t, op, sp.EventOutputClosed)
	after, closed := len(output(evs, sp.StreamStdout)), evs[len(evs)-1].(sp.OutputClosedEvent)
	if before != sandboxwire.MaxChunk || before+after != packet*packets || closed.Disposition != sp.OutputDrained {
		t.Fatalf("%d bytes before Exited and %d after, output %v", before, after, closed.Disposition)
	}
}

func TestStdinOffsetsAndHalfClose(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	ctx := context.Background()
	a := h.start(h.connect(), pipeSpec("cat"))
	if _, err := a.WriteStdin(ctx, []byte("ab")); err != nil {
		t.Fatal(err)
	}
	b, st, err := h.connect().Attach(ctx, h.svc.instance, a.Ref().OperationID, 0)
	if err != nil || st.StdinOffset != 2 {
		t.Fatalf("attach: %+v %v", st, err)
	}
	if _, err := a.WriteStdin(ctx, []byte("cd")); err != nil {
		t.Fatal(err)
	}
	_, err = b.WriteStdin(ctx, []byte("XX"))
	wantCode(t, err, sp.CodeInputOffsetConflict)
	if _, err := b.Inspect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteStdin(ctx, []byte("ef")); err != nil {
		t.Fatal(err)
	}
	for range 2 { // idempotent
		if err := b.CloseStdin(ctx); err != nil {
			t.Fatal(err)
		}
	}
	evs := events(t, b, sp.EventOutputClosed)
	if got := output(evs, sp.StreamStdout); got != "abcdef" {
		t.Fatalf("output %q", got)
	}
	_, err = b.WriteStdin(ctx, []byte("g"))
	wantCode(t, err, sp.CodeStdinClosed)
}

func TestPTYResize(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	ctx := context.Background()
	spec := ptySpec("sh", "-c", `echo "$TERM"; stty size; read line; stty size`)
	spec.PTY.Modes = []sp.PTYModeValue{{Mode: sp.ModeECHO, Value: 0}}
	op := h.start(h.connect(), spec)
	var seen string
	for !strings.Contains(seen, "24 80") {
		seen += output(events(t, op, sp.EventOutput), sp.StreamTerminal)
	}
	if err := op.Resize(ctx, sp.WindowSize{Rows: 40, Cols: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := op.WriteStdin(ctx, []byte("go\n")); err != nil {
		t.Fatal(err)
	}
	seen += output(events(t, op, sp.EventOutputClosed), sp.StreamTerminal)
	if !strings.Contains(seen, "xterm\r\n24 80\r\n40 100\r\n") || strings.Contains(seen, "go") {
		t.Fatalf("terminal output %q", seen)
	}
	wantCode(t, op.CloseStdin(ctx), sp.CodeUnsupported)
}

func TestSignalTargets(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	c := h.connect()
	ctx := context.Background()

	t.Run("leader", func(t *testing.T) {
		op := h.start(c, pipeSpec("sleep", "30"))
		if err := op.Signal(ctx, 15, sp.TargetLeader); err != nil {
			t.Fatal(err)
		}
		if e, _ := find[sp.ExitedEvent](t, events(t, op, sp.EventExited)); e.Status.Signal != 15 {
			t.Fatalf("exit %+v", e.Status)
		}
	})
	t.Run("initial process group", func(t *testing.T) {
		op := h.start(c, pipeSpec("sh", "-c", "sleep 30 & wait"))
		h.waitMembers(op, "sleep", 1)
		if err := op.Signal(ctx, 15, sp.TargetInitialProcessGroup); err != nil {
			t.Fatal(err)
		}
		events(t, op, sp.EventScopeClosed)
	})
	t.Run("PTY foreground group", func(t *testing.T) {
		op := h.start(c, ptySpec("bash", "-c", "set -m; sleep 30; echo after $?"))
		h.waitMembers(op, "sleep", 1)
		if err := op.Signal(ctx, 15, sp.TargetPTYForegroundGroup); err != nil {
			t.Fatal(err)
		}
		// The shell is outside the foreground job, so it outlives it.
		evs := events(t, op, sp.EventOutputClosed)
		if out := output(evs, sp.StreamTerminal); !strings.Contains(out, "after 143") {
			t.Fatalf("output %q", out)
		}
	})
	t.Run("scope", func(t *testing.T) {
		// Job control puts each sleep in its own process group.
		op := h.start(c, ptySpec("bash", "-c", "set -m; sleep 30 & sleep 30"))
		h.waitMembers(op, "sleep", 2)
		if err := op.Signal(ctx, 15, sp.TargetScope); err != nil {
			t.Fatal(err)
		}
		events(t, op, sp.EventScopeClosed)
	})
	t.Run("undeclared", func(t *testing.T) {
		op := h.start(c, pipeSpec("true"))
		wantCode(t, op.Signal(ctx, 11, sp.TargetLeader), sp.CodeUnsupported)
	})
}

func TestCancelEscalatesToKill(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	op := h.start(h.connect(), pipeSpec("sh", "-c", `trap "" TERM; sleep 30`))
	h.waitMembers(op, "sleep", 1)
	if err := op.Cancel(context.Background(), 200); err != nil {
		t.Fatal(err)
	}
	evs := events(t, op, sp.EventScopeClosed)
	if e, _ := find[sp.ExitedEvent](t, evs); e.Status.Signal != 9 {
		t.Fatalf("exit %+v", e.Status)
	}
}

func TestStartDeduplication(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	ctx := context.Background()
	id, spec := sandboxwire.NewID(), pipeSpec("echo", "once")
	if _, disp, err := h.connect().Start(ctx, h.svc.instance, id, spec); err != nil || disp != sp.StartCreated {
		t.Fatalf("start: %v %v", disp, err)
	}
	op, disp, err := h.connect().Start(ctx, h.svc.instance, id, spec)
	if err != nil || disp != sp.StartExisting {
		t.Fatalf("repeat: %v %v", disp, err)
	}
	if got := output(events(t, op, sp.EventOutputClosed), sp.StreamStdout); got != "once\n" {
		t.Fatalf("output %q", got)
	}
	_, _, err = h.connect().Start(ctx, h.svc.instance, id, pipeSpec("echo", "twice"))
	wantCode(t, err, sp.CodeOperationConflict)
}

func TestReleaseKeepsTombstone(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	ctx := context.Background()
	c := h.connect()

	busy := h.start(c, pipeSpec("sleep", "30"))
	wantCode(t, busy.Release(ctx), sp.CodeBusy)
	busy.Cancel(ctx, 0)

	spec := pipeSpec("true")
	op := h.start(c, spec)
	events(t, op, sp.EventScopeClosed)
	if err := op.Release(ctx); err != nil {
		t.Fatal(err)
	}
	c2 := h.connect()
	_, _, err := c2.Start(ctx, h.svc.instance, op.Ref().OperationID, spec)
	wantCode(t, err, sp.CodeReleased)
	_, _, err = c2.Attach(ctx, h.svc.instance, op.Ref().OperationID, 0)
	wantCode(t, err, sp.CodeReleased)
	h.svc.mu.Lock()
	status := h.svc.ops[opKey{h.att, op.Ref().OperationID}].inspect()
	h.svc.mu.Unlock()
	if !status.Released || status.State != sp.StateExited || status.Exit.Code != 0 {
		t.Fatalf("tombstone %+v", status)
	}
}

func TestAttachAfterAckIsReplayGap(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	ctx := context.Background()
	op := h.start(h.connect(), pipeSpec("echo", "hi"))
	evs := events(t, op, sp.EventOutputClosed)
	last := evs[len(evs)-1].Header().Sequence
	if err := op.Ack(ctx, last); err != nil {
		t.Fatal(err)
	}
	c := h.connect()
	_, _, err := c.Attach(ctx, h.svc.instance, op.Ref().OperationID, 0)
	wantCode(t, err, sp.CodeReplayGap)
	if _, st, err := c.Attach(ctx, h.svc.instance, op.Ref().OperationID, last); err != nil || st.FirstRetained != last+1 {
		t.Fatalf("attach after ack: %+v %v", st, err)
	}
}

// Unacknowledged output stops reading, so the writer blocks and nothing drops.
func TestOutputWaitsForAcks(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxReplayBytesPerOperation = sandboxwire.MaxChunk
	h := newHarness(t, cfg)
	ctx := context.Background()
	const size = 300000 // more than the replay limit plus a pipe buffer
	op := h.start(h.connect(), pipeSpec("head", "-c", strconv.Itoa(size), "/dev/zero"))
	time.Sleep(200 * time.Millisecond)
	if st, err := op.Inspect(ctx); err != nil || st.State != sp.StateRunning {
		t.Fatalf("before acknowledging: %+v %v", st, err)
	}
	var total uint64
	var exit *sp.ExitStatus
	for total < size || exit == nil {
		switch ev := next(t, op).(type) {
		case sp.OutputEvent:
			if ev.Offset != total {
				t.Fatalf("offset %d after %d bytes", ev.Offset, total)
			}
			total += uint64(len(ev.Data))
			if err := op.Ack(ctx, ev.Sequence); err != nil {
				t.Fatal(err)
			}
		case sp.ExitedEvent:
			exit = &ev.Status
		}
	}
	if exit.Code != 0 || total != size {
		t.Fatalf("exit %+v after %d bytes", exit, total)
	}
}

// Losing the stream alone never cancels; ownership loss does after the grace.
func TestOwnerLoss(t *testing.T) {
	cfg := DefaultConfig()
	cfg.OwnerLossGrace, cfg.CancelGraceLimit = 200*time.Millisecond, 200*time.Millisecond
	h := newHarness(t, cfg)
	ctx := context.Background()

	first := h.connect()
	id := h.start(first, pipeSpec("sleep", "30")).Ref().OperationID
	first.Close()
	h.svc.AttachmentLost(h.att)
	h.svc.AttachmentRestored(h.att)
	time.Sleep(2 * cfg.OwnerLossGrace)
	op, st, err := h.connect().Attach(ctx, h.svc.instance, id, 0)
	if err != nil || st.State != sp.StateRunning {
		t.Fatalf("after restore: %+v %v", st, err)
	}

	h.svc.AttachmentLost(h.att)
	if e, _ := find[sp.ExitedEvent](t, events(t, op, sp.EventExited)); e.Status.Signal != 15 {
		t.Fatalf("exit %+v", e.Status)
	}
}

func TestInstanceChanged(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	_, _, err := h.connect().Start(context.Background(), sandboxwire.NewID(), sandboxwire.NewID(), pipeSpec("true"))
	wantCode(t, err, sp.CodeInstanceChanged)
}

func TestCloseOutput(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	ctx := context.Background()
	op := h.start(h.connect(), pipeSpec("sh", "-c", "echo ready; read x; echo after"))
	events(t, op, sp.EventOutput)
	if err := op.CloseOutput(ctx, sp.StreamStdout); err != nil {
		t.Fatal(err)
	}
	wantCode(t, op.CloseOutput(ctx, sp.StreamStdout), sp.CodeOutputClosed)
	if _, err := op.WriteStdin(ctx, []byte("go\n")); err != nil {
		t.Fatal(err)
	}
	// The writer sees a closed pipe.
	evs := events(t, op, sp.EventScopeClosed)
	for _, ev := range evs {
		if c, ok := ev.(sp.StreamClosedEvent); ok && c.Stream == sp.StreamStdout && c.Disposition != sp.OutputAbandoned {
			t.Fatalf("stdout closed %+v", c)
		}
	}
	closed, _ := find[sp.OutputClosedEvent](t, evs)
	exited, _ := find[sp.ExitedEvent](t, evs)
	if closed.Disposition != sp.OutputAbandoned || exited.Status.Signal != 13 {
		t.Fatalf("events %v", evs)
	}
}

func TestCapacity(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxActiveOperations = 1
	h := newHarness(t, cfg)
	ctx := context.Background()
	c := h.connect()
	op := h.start(c, pipeSpec("sleep", "30"))
	_, _, err := c.Start(ctx, h.svc.instance, sandboxwire.NewID(), pipeSpec("true"))
	wantCode(t, err, sp.CodeResourceExhausted)
	op.Cancel(ctx, 0)
}

// A failed scope poll reports ObservationLost, and the operation stays
// unsettled until a later poll confirms the scope empty.
func TestScopeObservationLost(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	var failing atomic.Bool
	failing.Store(true)
	h.svc.stat = func(pid int) (procStat, error) {
		if failing.Load() {
			return procStat{}, errors.New("injected")
		}
		return readStat(pid)
	}
	ctx := context.Background()
	op := h.start(h.connect(), pipeSpec("true"))
	for lost, closed := false, false; !lost || !closed; {
		switch ev := next(t, op).(type) {
		case sp.ObservationLostEvent:
			if ev.Observation != sp.ObservationScope || ev.Failure.Effect != sandboxwire.EffectPossible {
				t.Fatalf("observation lost %+v", ev)
			}
			lost = true
		case sp.OutputClosedEvent:
			closed = true
		}
	}
	wantCode(t, op.Release(ctx), sp.CodeBusy)
	failing.Store(false)
	events(t, op, sp.EventScopeClosed)
	if err := op.Release(ctx); err != nil {
		t.Fatal(err)
	}
}

// Once the session leader exits, the terminal has no foreground group, and
// the signal must not reach process group 0, the service's own.
func TestForegroundGroupAfterLeaderExit(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	ctx := context.Background()
	op := h.start(h.connect(), ptySpec("sh", "-c", `trap "" HUP; sleep 30 & exit 0`))
	events(t, op, sp.EventExited)
	wantCode(t, op.Signal(ctx, 28, sp.TargetPTYForegroundGroup), sp.CodeNotRunning)
	if err := op.Cancel(ctx, 0); err != nil {
		t.Fatal(err)
	}
	events(t, op, sp.EventScopeClosed)
}

// Revocation that finds an operation still starting cancels it when the
// launch completes.
func TestRevokeWhileStarting(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	spec := pipeSpec("sleep", "30")
	key := opKey{h.att, sandboxwire.NewID()}
	op := newOperation(h.svc, key, spec.Digest())
	h.svc.mu.Lock()
	h.svc.ops[key] = op
	h.svc.active.Add(1)
	h.svc.mu.Unlock()
	h.svc.AttachmentRevoked(h.att)
	op.launch(sp.StartRequest{OperationRef: sp.OperationRef{ServerInstanceID: h.svc.instance, OperationID: key.operation}, Spec: spec})
	for deadline := time.Now().Add(10 * time.Second); op.inspect().State != sp.StateExited; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the launched operation was not cancelled")
		}
	}
	if exit := op.inspect().Exit; exit.Signal != 15 {
		t.Fatalf("exit %+v", exit)
	}
}

// waitDropped waits until the service's bookkeeping is back at a new
// service's: no records, owner-loss graces, stale attachments or active
// operations.
func (h *harness) waitDropped() {
	h.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
		h.svc.mu.Lock()
		ops, owners, stale := len(h.svc.ops), len(h.svc.owners), len(h.svc.stale)
		h.svc.mu.Unlock()
		active := h.svc.active.Load()
		if ops+owners+stale == 0 && active == 0 {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("%d records, %d graces, %d stale attachments, %d active operations remain", ops, owners, stale, active)
		}
	}
}

// Closed attachments leave nothing behind once their operations settle: the
// first is closed while its operation runs, the second after its operation
// ended, and the rest never started one.
func TestClosedAttachmentsAreDropped(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	for i := range 64 {
		a := &harness{t: t, svc: h.svc, att: sandboxwire.NewID()}
		if i < 2 {
			c := a.connect()
			if i == 0 {
				a.start(c, pipeSpec("sleep", "30"))
			} else {
				events(t, a.start(c, pipeSpec("true")), sp.EventScopeClosed)
			}
			c.Close()
		}
		h.svc.AttachmentLost(a.att)
		h.svc.AttachmentRevoked(a.att)
	}
	h.waitDropped()
}

// Revocation returns while the cancellation is still scanning /proc.
func TestRevokeDoesNotWaitForCancel(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	var armed atomic.Bool
	scanning, release := make(chan struct{}), make(chan struct{})
	scanned, unblock := sync.OnceFunc(func() { close(scanning) }), sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	h.svc.stat = func(pid int) (procStat, error) {
		if armed.Load() {
			scanned()
			<-release
		}
		return readStat(pid)
	}
	c := h.connect()
	h.start(c, pipeSpec("sleep", "30"))
	c.Close()
	armed.Store(true)
	revoked := make(chan struct{})
	go func() {
		h.svc.AttachmentRevoked(h.att)
		close(revoked)
	}()
	<-scanning
	select {
	case <-revoked:
	case <-time.After(10 * time.Second):
		t.Fatal("AttachmentRevoked waited for the cancellation")
	}
	unblock()
	h.waitDropped()
}

// connOf hands over each stream's Conn when the stream calls Describe.
type connOf struct {
	*Service
	conns chan *sp.Conn
}

func (c connOf) Describe(ctx context.Context, conn *sp.Conn, req sp.DescribeRequest) (sp.DescribeResponse, error) {
	c.conns <- conn
	return c.Service.Describe(ctx, conn, req)
}

// A request still running on a closed attachment's stream starts nothing,
// even once the attachment's entry is gone: the Link ends the stream's
// context before it reports the close.
func TestClosedAttachmentStartsNothing(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	server, client := net.Pipe()
	ctx, closeAttachment := context.WithCancel(context.Background())
	conns, served := make(chan *sp.Conn, 1), make(chan struct{})
	go func() {
		defer close(served)
		sp.Serve(ctx, server, sp.Attachment{ID: h.att}, connOf{h.svc, conns})
	}()
	c := sp.NewClient(client)
	t.Cleanup(func() {
		c.Close()
		<-served
	})
	if _, err := c.Describe(context.Background()); err != nil {
		t.Fatal(err)
	}
	conn := <-conns
	closeAttachment()
	h.svc.AttachmentRevoked(h.att)
	h.waitDropped()
	_, err := h.svc.Start(context.Background(), conn, sp.StartRequest{OperationRef: sp.OperationRef{ServerInstanceID: h.svc.instance, OperationID: sandboxwire.NewID()}, Spec: pipeSpec("true")})
	wantCode(t, err, sp.CodeStaleAttachment)
}

// An Ack from another stream cannot take events an accepted Attach promised.
func TestAckKeepsPromisedReplay(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	ctx := context.Background()
	a := h.start(h.connect(), pipeSpec("echo", "hi"))
	evs := events(t, a, sp.EventScopeClosed)
	last := evs[len(evs)-1].Header().Sequence

	// b attaches from the start and reads only the response, so the service
	// still holds its events when a acknowledges them all.
	server, b := net.Pipe()
	go sp.Serve(ctx, server, sp.Attachment{ID: h.att}, h.svc)
	t.Cleanup(func() { b.Close() })
	b.SetDeadline(time.Now().Add(10 * time.Second))
	read := func() sp.Message {
		t.Helper()
		f, err := sandboxwire.ReadFrame(b, sandboxwire.MaxPayload)
		if err != nil {
			t.Fatal(err)
		}
		m, err := sp.Decode(f.Type, f.Payload)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	req := sp.AttachRequest{OperationRef: a.Ref()}
	if err := sandboxwire.WriteFrame(b, sandboxwire.Frame{Type: sp.OpAttach, RequestID: 1, Payload: sp.Encode(req)}); err != nil {
		t.Fatal(err)
	}
	if m := read(); m.MessageType() != sandboxwire.ResponseType(sp.OpAttach) {
		t.Fatalf("attach: %+v", m)
	} else if _, ok := m.(sp.AttachResponse); !ok {
		t.Fatalf("attach: %+v", m)
	}
	if err := a.Ack(ctx, last); err != nil {
		t.Fatal(err)
	}
	for seq := uint64(1); seq <= last; seq++ {
		if ev, ok := read().(sp.Event); !ok || ev.Header().Sequence != seq {
			t.Fatalf("event %d: %v", seq, ev)
		}
	}
}

// The reap loop reaps an orphan reparented to the subreaper and still
// delivers the leader's status to its operation.
func TestReapsOrphans(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	op := h.start(h.connect(), pipeSpec("sh", "-c", "sleep 1 >/dev/null 2>&1 & echo $!; exit 3"))
	evs := events(t, op, sp.EventExited)
	if e, _ := find[sp.ExitedEvent](t, evs); e.Status != (sp.ExitStatus{Kind: sp.ExitCode, Code: 3}) {
		t.Fatalf("exit %+v", e.Status)
	}
	for !strings.Contains(output(evs, sp.StreamStdout), "\n") {
		evs = append(evs, next(t, op))
	}
	pid := strings.TrimSpace(output(evs, sp.StreamStdout))
	stat, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		t.Fatal(err)
	}
	if ppid := string(bytes.Fields(stat[bytes.LastIndexByte(stat, ')')+1:])[1]); ppid != strconv.Itoa(os.Getpid()) {
		t.Fatalf("orphan %s has parent %s", pid, ppid)
	}
	events(t, op, sp.EventScopeClosed)
	// ScopeClosed ignores zombies; the orphan must not stay one.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat("/proc/" + pid); errors.Is(err, fs.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphan %s was never reaped", pid)
		}
	}
}

// Once the session is empty and its leader reaped, its ID can name a new
// session. A process showing the ID then is not signaled: nothing in custody
// proves it is in the operation's session.
func TestSignalsNeedCustody(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	ctx := context.Background()
	c := h.connect()
	victim := h.start(c, pipeSpec("sleep", "30"))
	events(t, victim, sp.EventStarted)
	vpid := h.leader(victim)

	// The victim's custody keeps the real stat; the operation's reads
	// through the hook: first failing, then showing the victim in its session.
	var blind atomic.Bool
	var claim atomic.Int64
	blind.Store(true)
	h.svc.stat = func(pid int) (procStat, error) {
		if blind.Load() {
			return procStat{}, errors.New("injected")
		}
		st, err := readStat(pid)
		if sid := claim.Load(); sid != 0 && pid == vpid {
			st.session = int(sid)
		}
		return st, err
	}
	op := h.start(c, pipeSpec("true"))
	sid := h.leader(op)
	for lost := false; !lost; {
		_, lost = next(t, op).(sp.ObservationLostEvent)
	}
	claim.Store(int64(sid))
	blind.Store(false)

	wantCode(t, op.Signal(ctx, 9, sp.TargetScope), sp.CodeNotRunning)
	wantCode(t, op.Signal(ctx, 9, sp.TargetInitialProcessGroup), sp.CodeNotRunning)
	if err := op.Cancel(ctx, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // the KILL escalation polls meanwhile
	if st, err := victim.Inspect(ctx); err != nil || st.State != sp.StateRunning {
		t.Fatalf("victim %+v %v", st, err)
	}
	if st, err := op.Inspect(ctx); err != nil || st.Scope != sp.ScopeStateUnknown {
		t.Fatalf("scope %+v %v", st, err)
	}
	claim.Store(0)
	events(t, op, sp.EventScopeClosed)
	if err := victim.Cancel(ctx, 0); err != nil {
		t.Fatal(err)
	}
	events(t, victim, sp.EventScopeClosed)
}

// A held process that leaves the session is signaled only if a refresh
// proved it still there; a failed refresh signals nothing.
func TestFailedRefreshSignalsNothing(t *testing.T) {
	h := newHarness(t, DefaultConfig())
	var failing atomic.Bool
	h.svc.stat = func(pid int) (procStat, error) {
		if failing.Load() {
			return procStat{}, errors.New("injected")
		}
		return readStat(pid)
	}
	ctx := context.Background()
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	op := h.start(h.connect(), pipeSpec("sh", "-c", `(read x <"$0"; exec setsid sleep 30) & exit 0`, fifo))
	sid := h.leader(op)
	events(t, op, sp.EventExited) // the orphan joined custody before the leader was reaped

	orphan := 0
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		if pid, err := strconv.Atoi(e.Name()); err == nil {
			if st, err := readStat(pid); err == nil && st.session == sid && st.live() {
				orphan = pid
			}
		}
	}
	if orphan == 0 {
		t.Fatal("no orphan in the session")
	}
	t.Cleanup(func() { unix.Kill(orphan, unix.SIGKILL) })
	failing.Store(true)
	if err := os.WriteFile(fifo, []byte("\n"), 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		if st, err := readStat(orphan); err == nil && st.session == orphan {
			break
		} else if i == 500 {
			t.Fatalf("the orphan did not leave the session: %+v %v", st, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	wantCode(t, op.Signal(ctx, 19, sp.TargetScope), sp.CodeIO)
	time.Sleep(50 * time.Millisecond)
	if st, err := readStat(orphan); err != nil || st.state == 'T' {
		t.Fatalf("the escaped process was signaled: %+v %v", st, err)
	}
	failing.Store(false)
	events(t, op, sp.EventScopeClosed)
}

// A restore racing the grace expiry either stops it or follows the cleanup;
// it never ends with the restored attachment stale.
func TestRestoreRacesExpiry(t *testing.T) {
	cfg := DefaultConfig()
	cfg.OwnerLossGrace = time.Millisecond
	h := newHarness(t, cfg)
	restored := make(chan struct{})
	h.svc.onExpire = func() {
		h.svc.AttachmentRestored(h.att)
		close(restored)
	}
	h.svc.AttachmentLost(h.att)
	<-restored
	time.Sleep(20 * time.Millisecond) // lets a late cleanup land
	h.svc.mu.Lock()
	stale := h.svc.stale[h.att]
	h.svc.mu.Unlock()
	if stale {
		t.Fatal("the restored attachment is stale")
	}
}

// A traced child's ptrace stop reaches the reaper even without WUNTRACED. It
// is not an exit: the leader stays registered and nothing is reported.
func TestReaperIgnoresStops(t *testing.T) {
	op := newOperation(nil, opKey{}, [32]byte{})
	const pid = 1 << 30 // above any pid_max
	register(pid, op)
	registered := func() bool {
		regMu.Lock()
		defer regMu.Unlock()
		return leaders[pid] == op
	}
	dispatch(pid, unix.WaitStatus(unix.SIGTRAP<<8|0x7f))
	if !registered() || op.leaderGone {
		t.Fatal("a stop ended the registration")
	}
	dispatch(pid, unix.WaitStatus(3<<8))
	if registered() || !op.leaderGone || op.status.ExitStatus() != 3 {
		t.Fatalf("exit not delivered: %v", op.status)
	}
}
