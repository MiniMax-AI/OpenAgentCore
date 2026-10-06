//go:build linux

package processbroker

import (
	"context"
	"io"
	"net"
	"slices"
	"sync"
	"sync/atomic"

	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// peer is a process service with a single operation, scripted for the
// faults the uncertain-request tests need. Its program copies stdin to
// stdout and exits 0 when stdin closes.
type peer struct {
	// startLate keeps the operation Starting until runStarting, or until
	// stdin is written, which it refuses. attachedStarting closes once an
	// Attach finds the operation Starting.
	startLate        bool
	attachedStarting chan struct{}
	startFails       bool // the start ends with StartFailed
	partialFirst     bool // the first stdin write is half accepted
	inspectFails     bool // Inspect fails for good
	// leaderExits exits the leader with 0 as it takes the first stdin
	// write. A background process it leaves copies the rest of stdin to
	// stdout until end of file.
	leaderExits bool

	instance sandboxwire.ID

	mu sync.Mutex
	seen
	id         sandboxwire.ID
	events     []sp.Event // events[i] has sequence i+1
	out        net.Conn   // the stream subscribed to the events
	background bool       // a background process outlives the leader
}

// seen is what the peer saw.
type seen struct {
	st                         sp.OperationStatus
	stdin                      []byte // the stdin the service took
	writes, cancels, refusedIn int    // refusedIn counts stdin refused while Starting
}

func newPeer() *peer {
	return &peer{instance: sandboxwire.NewID(), attachedStarting: make(chan struct{}),
		seen: seen{st: sp.OperationStatus{State: sp.StateStarting, Scope: sp.ScopeStateActive, FirstRetained: 1}}}
}

func (p *peer) counts() seen {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.seen
	s.stdin = slices.Clone(s.stdin)
	return s
}

// dial serves each stream the broker dials. The first stream is lost in
// place of the response to its first request of type lose.
func (p *peer) dial(lose uint16) func(context.Context) (io.ReadWriteCloser, error) {
	var dials atomic.Int32
	return func(context.Context) (io.ReadWriteCloser, error) {
		broker, svc := net.Pipe()
		go p.serve(svc, dials.Add(1) == 1, lose)
		return broker, nil
	}
}

func (p *peer) serve(c net.Conn, first bool, lose uint16) {
	defer c.Close()
	for {
		f, err := sandboxwire.ReadFrame(c, sandboxwire.MaxPayload)
		if err != nil {
			return
		}
		m, err := sp.Decode(f.Type, f.Payload)
		if err != nil {
			return
		}
		p.mu.Lock()
		resp, failure := p.handle(m)
		if first && f.Type == lose {
			p.mu.Unlock()
			return
		}
		if failure != nil {
			resp = sp.ResponseFailure{Request: f.Type, Failure: *failure}
		}
		send(c, f.RequestID, resp)
		switch m := m.(type) {
		case sp.StartRequest:
			if resp == (sp.StartResponse{Disposition: sp.StartCreated}) {
				p.subscribe(c, 0)
			}
		case sp.AttachRequest:
			p.subscribe(c, m.AfterSequence)
		}
		p.mu.Unlock()
	}
}

// subscribe sends c the events after seq, and every later one; p.mu is
// held.
func (p *peer) subscribe(c net.Conn, seq uint64) {
	p.out = c
	for _, ev := range p.events[seq:] {
		send(c, 0, ev)
	}
}

func send(c net.Conn, requestID uint64, m sp.Message) {
	sandboxwire.WriteFrame(c, sandboxwire.Frame{Type: m.MessageType(), RequestID: requestID, Payload: sp.Encode(m)})
}

// handle applies one request; p.mu is held.
func (p *peer) handle(m sp.Message) (sp.Message, *sp.Failure) {
	switch m := m.(type) {
	case sp.DescribeRequest:
		return sp.DescribeResponse{ServerInstanceID: p.instance, Capabilities: sp.Capabilities{
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
		}}, nil
	case sp.StartRequest:
		if !p.id.IsZero() {
			return sp.StartResponse{Disposition: sp.StartExisting}, nil
		}
		p.id = m.OperationID
		if !p.startLate {
			p.run()
		}
		return sp.StartResponse{Disposition: sp.StartCreated}, nil
	case sp.AttachRequest:
		if p.st.State == sp.StateStarting {
			select {
			case <-p.attachedStarting:
			default:
				close(p.attachedStarting)
			}
		}
		return sp.AttachResponse{Status: p.st}, nil
	case sp.InspectRequest:
		if p.inspectFails {
			return nil, sp.Fail(sp.CodeIO, sandboxwire.EffectNone, "inspect failed")
		}
		return sp.InspectResponse{Status: p.st}, nil
	case sp.WriteStdinRequest:
		if f := p.refuseStdin(m.Offset); f != nil {
			return nil, f
		}
		data := m.Data
		if p.writes++; p.writes == 1 && p.partialFirst {
			data = data[:len(data)/2]
		}
		p.stdin = append(p.stdin, data...)
		if len(data) > 0 {
			out, echo := p.st.StdinOffset, slices.Clone(data)
			p.emit(func(h sp.EventHeader) sp.Event {
				return sp.OutputEvent{EventHeader: h, Stream: sp.StreamStdout, Offset: out, Data: echo}
			})
		}
		p.st.StdinOffset += uint64(len(data))
		if p.leaderExits && p.st.State == sp.StateRunning {
			status := sp.ExitStatus{Kind: sp.ExitCode}
			p.st.State, p.st.Exit, p.background = sp.StateExited, &status, true
			p.emit(func(h sp.EventHeader) sp.Event { return sp.ExitedEvent{EventHeader: h, Status: status} })
		}
		return sp.WriteStdinResponse{Accepted: uint32(len(data))}, nil
	case sp.CloseStdinRequest:
		if f := p.refuseStdin(m.Offset); f != nil {
			return nil, f
		}
		p.st.StdinClosed = true
		p.exit(sp.ExitStatus{Kind: sp.ExitCode})
		return sp.CloseStdinResponse{}, nil
	case sp.CancelRequest:
		p.cancels++
		p.exit(sp.ExitStatus{Kind: sp.ExitSignal, Signal: 15})
		return sp.CancelResponse{}, nil
	case sp.AckEventsRequest:
		return sp.AckEventsResponse{}, nil
	case sp.ReleaseRequest:
		if p.st.Scope != sp.ScopeStateClosed {
			return nil, sp.Fail(sp.CodeBusy, sandboxwire.EffectNone, "not settled")
		}
		p.st.Released = true
		return sp.ReleaseResponse{}, nil
	}
	return nil, sp.Fail(sp.CodeUnsupported, sandboxwire.EffectNone, "not scripted")
}

// refuseStdin refuses stdin while the operation starts, which also ends a
// late start; p.mu is held.
func (p *peer) refuseStdin(offset uint64) *sp.Failure {
	switch {
	case p.st.State == sp.StateStarting:
		p.refusedIn++
		p.run()
		return sp.Fail(sp.CodeNotRunning, sandboxwire.EffectNone, "the operation is starting")
	case p.st.State != sp.StateRunning && !p.background:
		return sp.Fail(sp.CodeNotRunning, sandboxwire.EffectNone, "the operation is not running")
	case p.st.StdinClosed:
		return sp.Fail(sp.CodeStdinClosed, sandboxwire.EffectNone, "stdin is closed")
	case offset != p.st.StdinOffset:
		return sp.Fail(sp.CodeInputOffsetConflict, sandboxwire.EffectNone, "offset %d is not %d", offset, p.st.StdinOffset)
	}
	return nil
}

// runStarting ends a start in progress.
func (p *peer) runStarting() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.run()
}

// run ends the start; p.mu is held.
func (p *peer) run() {
	switch {
	case p.st.State != sp.StateStarting:
	case p.startFails:
		f := *sp.Fail(sp.CodeNotFound, sandboxwire.EffectNone, "no such file")
		d := sp.OutputDrained
		p.st.State, p.st.StartFailure, p.st.Output, p.st.Scope = sp.StateStartFailed, &f, &d, sp.ScopeStateClosed
		p.emit(func(h sp.EventHeader) sp.Event { return sp.StartFailedEvent{EventHeader: h, Failure: f} })
	default:
		p.st.State = sp.StateRunning
		p.emit(func(h sp.EventHeader) sp.Event { return sp.StartedEvent{EventHeader: h} })
	}
}

// exit ends the program with status, or only its background process when
// the leader already exited: the output and then the scope close, with
// the leader's exit between them when it exits now; p.mu is held.
func (p *peer) exit(status sp.ExitStatus) {
	leader := p.st.State == sp.StateRunning
	if !leader && !p.background {
		return
	}
	p.background = false
	out, d := p.st.StdinOffset, sp.OutputDrained
	p.emit(func(h sp.EventHeader) sp.Event {
		return sp.StreamClosedEvent{EventHeader: h, Stream: sp.StreamStdout, Offset: out, Disposition: d}
	})
	p.emit(func(h sp.EventHeader) sp.Event {
		return sp.StreamClosedEvent{EventHeader: h, Stream: sp.StreamStderr, Disposition: d}
	})
	if leader {
		p.st.State, p.st.Exit = sp.StateExited, &status
		p.emit(func(h sp.EventHeader) sp.Event { return sp.ExitedEvent{EventHeader: h, Status: status} })
	}
	p.emit(func(h sp.EventHeader) sp.Event { return sp.OutputClosedEvent{EventHeader: h, Disposition: d} })
	p.emit(func(h sp.EventHeader) sp.Event { return sp.ScopeClosedEvent{EventHeader: h} })
	p.st.Output, p.st.Scope = &d, sp.ScopeStateClosed
}

// emit records an event and sends it to the subscribed stream; p.mu is
// held.
func (p *peer) emit(ev func(sp.EventHeader) sp.Event) {
	e := ev(sp.EventHeader{OperationID: p.id, Sequence: uint64(len(p.events) + 1)})
	p.events = append(p.events, e)
	p.st.LastSequence++
	if p.out != nil {
		send(p.out, 0, e)
	}
}
