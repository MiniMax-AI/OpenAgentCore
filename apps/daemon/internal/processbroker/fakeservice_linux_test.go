//go:build linux

package processbroker

import (
	"context"
	"io"
	"net"
	"slices"
	"sync"

	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// fakeService is a process service whose program copies stdin to stdout and
// exits 0 when stdin closes. Its knobs reproduce one service behavior each.
type fakeService struct {
	instance sandboxwire.ID
	caps     sp.Capabilities

	// startLate keeps an operation Starting until runStarting, or until
	// stdin is written, which it refuses. attachedStarting closes once an
	// Attach finds the operation Starting.
	startLate        bool
	attachedStarting chan struct{}
	// startFails ends the start with StartFailed instead of Started.
	startFails bool
	// partialFirst accepts half of the first stdin write.
	partialFirst bool
	// inspectFails refuses Inspect for good.
	inspectFails bool
	// leaderExits exits the leader with 0 as it takes the first stdin
	// write. A background process it leaves copies the rest of stdin to
	// stdout until end of file.
	leaderExits bool

	mu        sync.Mutex
	ops       map[sandboxwire.ID]*fakeOp
	writes    int // stdin writes the service took
	cancels   int
	refusedIn int // stdin requests refused while Starting
}

type fakeOp struct {
	id          sandboxwire.ID
	state       sp.OperationState
	events      []sp.Event // events[i] has sequence i+1
	changed     chan struct{}
	stdin       []byte
	stdinClosed bool
	closedAt    uint64 // the CloseStdin offset
	stdout      uint64 // the stdout offset
	exit        *sp.ExitStatus
	failure     *sp.Failure
	background  bool // a background process outlives the leader
	released    bool
}

func newFakeService() *fakeService {
	return &fakeService{
		instance: sandboxwire.NewID(),
		caps: sp.Capabilities{
			Platform:                   sp.PlatformLinux,
			Scopes:                     []sp.Scope{sp.ScopePOSIXSession},
			IOModes:                    []sp.IOMode{sp.IOPipes},
			Signals:                    []sp.Signal{1, 2, 15},
			SignalTargets:              []sp.SignalTarget{sp.TargetInitialProcessGroup},
			MaxStartBytes:              sandboxwire.MaxPayload,
			MaxDataBytes:               sandboxwire.MaxChunk,
			MaxActiveOperations:        8,
			MaxOperationRecords:        8,
			MaxReplayBytesPerOperation: 1 << 20,
			OwnerLossGraceMillis:       60000,
			CancelGraceLimitMillis:     60000,
		},
		ops:              map[sandboxwire.ID]*fakeOp{},
		attachedStarting: make(chan struct{}),
	}
}

// serve replaces each stream to the real service with one to s, and loses
// the first stream in place of the response to the first request that lose
// selects.
func (s *fakeService) serve(ctx context.Context, lose func(sandboxwire.Frame) bool) func(int32, net.Conn) io.ReadWriteCloser {
	return func(n int32, c net.Conn) io.ReadWriteCloser {
		c.Close()
		broker, svc := net.Pipe()
		go sp.Serve(ctx, svc, sp.Attachment{ID: s.instance}, s)
		if n > 1 || lose == nil {
			return broker
		}
		return intercept(broker, func(fr sandboxwire.Frame) verdict {
			if lose(fr) {
				return loseResponse
			}
			return pass
		})
	}
}

// emit appends an event; s.mu is held.
func (s *fakeService) emit(op *fakeOp, ev func(sp.EventHeader) sp.Event) {
	op.events = append(op.events, ev(sp.EventHeader{OperationID: op.id, Sequence: uint64(len(op.events) + 1)}))
	close(op.changed)
	op.changed = make(chan struct{})
}

// subscribe sends op's events after seq on conn until the stream ends.
func (s *fakeService) subscribe(conn *sp.Conn, op *fakeOp, after uint64) {
	go func() {
		next := after
		for {
			s.mu.Lock()
			evs := slices.Clone(op.events[min(next, uint64(len(op.events))):])
			changed := op.changed
			s.mu.Unlock()
			for _, ev := range evs {
				if conn.Send(ev) != nil {
					return
				}
				next++
			}
			select {
			case <-changed:
			case <-conn.Context().Done():
				return
			}
		}
	}()
}

// run ends the start; s.mu is held.
func (s *fakeService) run(op *fakeOp) {
	if op.state != sp.StateStarting {
		return
	}
	if s.startFails {
		op.state = sp.StateStartFailed
		op.failure = sp.Fail(sp.CodeNotFound, sandboxwire.EffectNone, "no such file")
		f := *op.failure
		s.emit(op, func(h sp.EventHeader) sp.Event { return sp.StartFailedEvent{EventHeader: h, Failure: f} })
		return
	}
	op.state = sp.StateRunning
	s.emit(op, func(h sp.EventHeader) sp.Event { return sp.StartedEvent{EventHeader: h} })
}

// runStarting ends every start in progress.
func (s *fakeService) runStarting() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, op := range s.ops {
		s.run(op)
	}
}

// exited ends the program with status, or only its background process
// when the leader already exited; s.mu is held.
func (s *fakeService) exited(op *fakeOp, status sp.ExitStatus) {
	switch {
	case op.background:
		op.background = false
		s.drained(op, nil)
	case op.state == sp.StateRunning:
		op.state, op.exit = sp.StateExited, &status
		s.drained(op, &status)
	}
}

// drained closes the output and the scope, with the leader's exit between
// them when it exits now; s.mu is held.
func (s *fakeService) drained(op *fakeOp, exit *sp.ExitStatus) {
	out := op.stdout
	s.emit(op, func(h sp.EventHeader) sp.Event {
		return sp.StreamClosedEvent{EventHeader: h, Stream: sp.StreamStdout, Offset: out, Disposition: sp.OutputDrained}
	})
	s.emit(op, func(h sp.EventHeader) sp.Event {
		return sp.StreamClosedEvent{EventHeader: h, Stream: sp.StreamStderr, Disposition: sp.OutputDrained}
	})
	if exit != nil {
		s.emit(op, func(h sp.EventHeader) sp.Event { return sp.ExitedEvent{EventHeader: h, Status: *exit} })
	}
	s.emit(op, func(h sp.EventHeader) sp.Event {
		return sp.OutputClosedEvent{EventHeader: h, Disposition: sp.OutputDrained}
	})
	s.emit(op, func(h sp.EventHeader) sp.Event { return sp.ScopeClosedEvent{EventHeader: h} })
}

func (s *fakeService) status(op *fakeOp) sp.OperationStatus {
	st := sp.OperationStatus{
		State: op.state, Exit: op.exit, StartFailure: op.failure,
		StdinOffset: uint64(len(op.stdin)), StdinClosed: op.stdinClosed,
		Scope: sp.ScopeStateActive, Released: op.released,
		FirstRetained: 1, LastSequence: uint64(len(op.events)),
	}
	if op.state == sp.StateExited && !op.background || op.state == sp.StateStartFailed {
		d := sp.OutputDrained
		st.Output, st.Scope = &d, sp.ScopeStateClosed
	}
	return st
}

// lookup returns the operation with s.mu held.
func (s *fakeService) lookup(ref sp.OperationRef) (*fakeOp, error) {
	if ref.ServerInstanceID != s.instance {
		return nil, sp.Fail(sp.CodeInstanceChanged, sandboxwire.EffectNone, "instance changed")
	}
	s.mu.Lock()
	op := s.ops[ref.OperationID]
	if op == nil {
		s.mu.Unlock()
		return nil, sp.Fail(sp.CodeNotFound, sandboxwire.EffectNone, "no operation")
	}
	if op.released {
		s.mu.Unlock()
		return nil, sp.Fail(sp.CodeReleased, sandboxwire.EffectNone, "released")
	}
	return op, nil
}

func (s *fakeService) Describe(context.Context, *sp.Conn, sp.DescribeRequest) (sp.DescribeResponse, error) {
	return sp.DescribeResponse{ServerInstanceID: s.instance, Capabilities: s.caps}, nil
}

func (s *fakeService) Start(_ context.Context, conn *sp.Conn, req sp.StartRequest) (sp.StartResponse, error) {
	if req.ServerInstanceID != s.instance {
		return sp.StartResponse{}, sp.Fail(sp.CodeInstanceChanged, sandboxwire.EffectNone, "instance changed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ops[req.OperationID] != nil {
		return sp.StartResponse{Disposition: sp.StartExisting}, nil
	}
	op := &fakeOp{id: req.OperationID, state: sp.StateStarting, changed: make(chan struct{})}
	s.ops[op.id] = op
	s.subscribe(conn, op, 0)
	if !s.startLate {
		s.run(op)
	}
	return sp.StartResponse{Disposition: sp.StartCreated}, nil
}

func (s *fakeService) Attach(_ context.Context, conn *sp.Conn, req sp.AttachRequest) (sp.AttachResponse, error) {
	op, err := s.lookup(req.OperationRef)
	if err != nil {
		return sp.AttachResponse{}, err
	}
	defer s.mu.Unlock()
	s.subscribe(conn, op, req.AfterSequence)
	if op.state == sp.StateStarting {
		select {
		case <-s.attachedStarting:
		default:
			close(s.attachedStarting)
		}
	}
	return sp.AttachResponse{Status: s.status(op)}, nil
}

func (s *fakeService) Inspect(_ context.Context, _ *sp.Conn, req sp.InspectRequest) (sp.InspectResponse, error) {
	op, err := s.lookup(req.OperationRef)
	if err != nil {
		return sp.InspectResponse{}, err
	}
	defer s.mu.Unlock()
	if s.inspectFails {
		return sp.InspectResponse{}, sp.Fail(sp.CodeIO, sandboxwire.EffectNone, "inspect failed")
	}
	return sp.InspectResponse{Status: s.status(op)}, nil
}

// stdinRefused refuses stdin while the operation starts, which also ends a
// late start; s.mu is held.
func (s *fakeService) stdinRefused(op *fakeOp) error {
	switch {
	case op.state == sp.StateStarting:
		s.refusedIn++
		s.run(op)
		return sp.Fail(sp.CodeNotRunning, sandboxwire.EffectNone, "the operation is starting")
	case op.state != sp.StateRunning && !op.background:
		return sp.Fail(sp.CodeNotRunning, sandboxwire.EffectNone, "the operation is not running")
	case op.stdinClosed:
		return sp.Fail(sp.CodeStdinClosed, sandboxwire.EffectNone, "stdin is closed")
	}
	return nil
}

func (s *fakeService) WriteStdin(_ context.Context, _ *sp.Conn, req sp.WriteStdinRequest) (sp.WriteStdinResponse, error) {
	op, err := s.lookup(req.OperationRef)
	if err != nil {
		return sp.WriteStdinResponse{}, err
	}
	defer s.mu.Unlock()
	if err := s.stdinRefused(op); err != nil {
		return sp.WriteStdinResponse{}, err
	}
	if req.Offset != uint64(len(op.stdin)) {
		return sp.WriteStdinResponse{}, sp.Fail(sp.CodeInputOffsetConflict, sandboxwire.EffectNone, "offset %d is not %d", req.Offset, len(op.stdin))
	}
	data := req.Data
	if s.writes++; s.writes == 1 && s.partialFirst {
		data = data[:len(data)/2]
	}
	op.stdin = append(op.stdin, data...)
	if len(data) > 0 {
		out, echo := op.stdout, slices.Clone(data)
		op.stdout += uint64(len(data))
		s.emit(op, func(h sp.EventHeader) sp.Event {
			return sp.OutputEvent{EventHeader: h, Stream: sp.StreamStdout, Offset: out, Data: echo}
		})
	}
	if s.leaderExits && op.state == sp.StateRunning {
		status := sp.ExitStatus{Kind: sp.ExitCode}
		op.state, op.exit, op.background = sp.StateExited, &status, true
		s.emit(op, func(h sp.EventHeader) sp.Event { return sp.ExitedEvent{EventHeader: h, Status: status} })
	}
	return sp.WriteStdinResponse{Accepted: uint32(len(data))}, nil
}

func (s *fakeService) CloseStdin(_ context.Context, _ *sp.Conn, req sp.CloseStdinRequest) (sp.CloseStdinResponse, error) {
	op, err := s.lookup(req.OperationRef)
	if err != nil {
		return sp.CloseStdinResponse{}, err
	}
	defer s.mu.Unlock()
	if op.stdinClosed && req.Offset == op.closedAt {
		return sp.CloseStdinResponse{}, nil
	}
	if err := s.stdinRefused(op); err != nil {
		return sp.CloseStdinResponse{}, err
	}
	if req.Offset != uint64(len(op.stdin)) {
		return sp.CloseStdinResponse{}, sp.Fail(sp.CodeInputOffsetConflict, sandboxwire.EffectNone, "offset %d is not %d", req.Offset, len(op.stdin))
	}
	op.stdinClosed, op.closedAt = true, req.Offset
	s.exited(op, sp.ExitStatus{Kind: sp.ExitCode})
	return sp.CloseStdinResponse{}, nil
}

func (s *fakeService) CloseOutput(_ context.Context, _ *sp.Conn, req sp.CloseOutputRequest) (sp.CloseOutputResponse, error) {
	if _, err := s.lookup(req.OperationRef); err != nil {
		return sp.CloseOutputResponse{}, err
	}
	s.mu.Unlock()
	return sp.CloseOutputResponse{}, nil
}

func (s *fakeService) ResizePTY(context.Context, *sp.Conn, sp.ResizePTYRequest) (sp.ResizePTYResponse, error) {
	return sp.ResizePTYResponse{}, sp.Fail(sp.CodeUnsupported, sandboxwire.EffectNone, "no terminal")
}

func (s *fakeService) Signal(_ context.Context, _ *sp.Conn, req sp.SignalRequest) (sp.SignalResponse, error) {
	if _, err := s.lookup(req.OperationRef); err != nil {
		return sp.SignalResponse{}, err
	}
	s.mu.Unlock()
	return sp.SignalResponse{}, nil
}

// Cancel ends the program as TERM does.
func (s *fakeService) Cancel(_ context.Context, _ *sp.Conn, req sp.CancelRequest) (sp.CancelResponse, error) {
	op, err := s.lookup(req.OperationRef)
	if err != nil {
		return sp.CancelResponse{}, err
	}
	defer s.mu.Unlock()
	s.cancels++
	s.exited(op, sp.ExitStatus{Kind: sp.ExitSignal, Signal: 15})
	return sp.CancelResponse{}, nil
}

func (s *fakeService) AckEvents(_ context.Context, _ *sp.Conn, req sp.AckEventsRequest) (sp.AckEventsResponse, error) {
	if _, err := s.lookup(req.OperationRef); err != nil {
		return sp.AckEventsResponse{}, err
	}
	s.mu.Unlock()
	return sp.AckEventsResponse{}, nil
}

func (s *fakeService) Release(_ context.Context, _ *sp.Conn, req sp.ReleaseRequest) (sp.ReleaseResponse, error) {
	op, err := s.lookup(req.OperationRef)
	if err != nil {
		return sp.ReleaseResponse{}, err
	}
	defer s.mu.Unlock()
	if op.state != sp.StateExited && op.state != sp.StateStartFailed || op.background {
		return sp.ReleaseResponse{}, sp.Fail(sp.CodeBusy, sandboxwire.EffectNone, "not settled")
	}
	op.released = true
	return sp.ReleaseResponse{}, nil
}

// fakeCounts is what a fakeService saw.
type fakeCounts struct {
	writes, cancels, refusedIn int
	stdin                      []byte // the only operation's accepted stdin
	closedAt                   uint64 // and its CloseStdin offset
	released                   bool   // and whether it was released
}

func (s *fakeService) counts() fakeCounts {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := fakeCounts{writes: s.writes, cancels: s.cancels, refusedIn: s.refusedIn}
	for _, op := range s.ops {
		c.stdin, c.closedAt, c.released = slices.Clone(op.stdin), op.closedAt, op.released
	}
	return c
}
