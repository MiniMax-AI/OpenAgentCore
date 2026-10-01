//go:build linux

package processbroker

import (
	"fmt"
	"slices"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// tracker records which events reached their destination. AckEvents
// follows its contiguous prefix.
type tracker struct {
	mu      sync.Mutex
	prefix  uint64
	done    map[uint64]bool
	changed chan struct{}
}

func (t *tracker) init() {
	t.done = map[uint64]bool{}
	t.changed = make(chan struct{})
}

func (t *tracker) deliver(seq uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if seq <= t.prefix {
		return
	}
	t.done[seq] = true
	start := t.prefix
	for t.done[t.prefix+1] {
		delete(t.done, t.prefix+1)
		t.prefix++
	}
	if t.prefix != start {
		close(t.changed)
		t.changed = make(chan struct{})
	}
}

func (t *tracker) state() (uint64, <-chan struct{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.prefix, t.changed
}

// wait waits until every event through seq is delivered.
func (t *tracker) wait(seq uint64, halt <-chan struct{}) bool {
	for {
		prefix, changed := t.state()
		if prefix >= seq {
			return true
		}
		select {
		case <-changed:
		case <-halt:
			return false
		}
	}
}

// chunk is an output event for a writer: data, or the stream's end.
type chunk struct {
	seq   uint64
	data  []byte
	close bool
}

// sent is Output or a Close the relay has not reported yet.
type sent struct {
	seq uint64
	n   int
}

// writer forwards one remote stream to its descriptor through the relay.
// At most processshim.OutputWindow bytes are unreported, so a descriptor the
// Harness does not read holds the stream back, and the process service in
// turn holds back the program. The relay closes the descriptor after the
// stream's last byte; on a PTY it also closes descriptor 2, which the merged
// stream never uses.
type writer struct {
	inv    *invocation
	stream sp.Stream
	fd     uint8

	mu     sync.Mutex
	queue  []chunk
	wake   chan struct{} // a push, or window space
	window int
	sent   []sent
	broken bool   // the relay reported a failed write
	last   uint64 // the last pushed chunk the relay reports
}

func (w *writer) push(c chunk) {
	w.mu.Lock()
	w.queue = append(w.queue, c)
	if c.close || len(c.data) > 0 {
		w.last = c.seq
	}
	w.mu.Unlock()
	w.signal()
}

func (w *writer) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// next returns the next chunk once the window has room for it, and whether
// the relay is to report it.
func (w *writer) next() (c chunk, report, ok bool) {
	for {
		w.mu.Lock()
		if len(w.queue) > 0 {
			c := w.queue[0]
			n := len(c.data)
			if w.broken || c.close || w.window == 0 || w.window+n <= processshim.OutputWindow {
				w.queue[0] = chunk{}
				w.queue = w.queue[1:]
				report := !w.broken && (c.close || n > 0)
				if report {
					w.sent = append(w.sent, sent{seq: c.seq, n: n})
					w.window += n
				}
				w.mu.Unlock()
				return c, report, true
			}
		}
		w.mu.Unlock()
		select {
		case <-w.wake:
		case <-w.inv.halt:
			return chunk{}, false, false
		}
	}
}

func (w *writer) run() {
	inv := w.inv
	defer inv.writing.Done()
	for {
		c, report, ok := w.next()
		if !ok {
			return
		}
		switch {
		case c.close:
			// After a failed write the relay still closes the descriptor,
			// without a report.
			inv.send(processshim.Close{ID: inv.rid, FD: w.fd, Seq: c.seq})
			if !report {
				inv.acks.deliver(c.seq)
			}
			return
		case report:
			inv.send(processshim.Output{ID: inv.rid, FD: w.fd, Seq: c.seq, Data: c.data})
		default:
			inv.acks.deliver(c.seq)
		}
	}
}

// marks returns, for each writer the relay still reports for, the last
// chunk pushed so far.
func (inv *invocation) marks() []processshim.Mark {
	var marks []processshim.Mark
	for _, w := range inv.byFD {
		if w == nil {
			continue
		}
		w.mu.Lock()
		if !w.broken && w.last > 0 {
			marks = append(marks, processshim.Mark{FD: w.fd, Seq: w.last})
		}
		w.mu.Unlock()
	}
	return marks
}

// written takes the relay's report of the oldest unreported chunk.
func (w *writer) written(seq uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.sent) == 0 || w.sent[0].seq != seq {
		return fmt.Errorf("%w: fd %d reported %d out of order", processshim.ErrProtocol, w.fd, seq)
	}
	w.window -= w.sent[0].n
	w.sent = w.sent[1:]
	w.signal()
	return nil
}

// failed takes the relay's report that the oldest unreported chunk failed,
// and returns every unreported chunk, none of which the relay reports.
func (w *writer) failed(seq uint64) ([]uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.sent) == 0 || w.sent[0].seq != seq {
		return nil, fmt.Errorf("%w: fd %d reported %d out of order", processshim.ErrProtocol, w.fd, seq)
	}
	seqs := make([]uint64, len(w.sent))
	for i, s := range w.sent {
		seqs[i] = s.seq
	}
	w.sent, w.window, w.broken = nil, 0, true
	w.signal()
	return seqs, nil
}

func (inv *invocation) closeOutput(stream sp.Stream) {
	inv.request(inv.current(), true, func(h handle) error { return h.op.CloseOutput(inv.b.ctx, stream) })
}

// pumpStdin forwards stdin until end of file, the exit, the shim's loss or
// the end. The relay reads descriptor 0 once per Read, so stdin is taken
// only as fast as the service accepts it.
func (inv *invocation) pumpStdin(caps sp.Capabilities) {
	limit := min(caps.MaxDataBytes, sandboxwire.MaxChunk)
	for {
		if !inv.grant(limit) {
			inv.stdinStopped()
			return
		}
		var m processshim.RelayMessage
		select {
		case m = <-inv.input:
		case <-inv.stopIn:
			inv.stdinStopped()
			return
		}
		select {
		case <-inv.stopIn:
			inv.stdinStopped()
			return
		default:
		}
		in, ok := m.(processshim.Input)
		if !ok { // end of file
			if inv.term == nil {
				inv.closeStdin()
			}
			return
		}
		if !inv.writeStdin(in.Data) {
			inv.stopInput()
			return
		}
	}
}

// grant asks the relay for one read of stdin.
func (inv *invocation) grant(limit uint32) bool {
	select {
	case <-inv.stopIn:
		return false
	default:
	}
	inv.mu.Lock()
	inv.credit = limit
	inv.mu.Unlock()
	return inv.send(processshim.Read{ID: inv.rid, Max: limit}) == nil
}

// stdinStopped closes the remote stdin after the exit, so remote background
// readers see end of file rather than wait for input the Harness no longer
// sends.
func (inv *invocation) stdinStopped() {
	if inv.term == nil && inv.exitDecided() {
		inv.closeStdin()
	}
}

// writeStdin writes data at the tracked offset. A write that had no effect
// continues on the next stream, and a Busy refusal after a backoff, from
// the first byte the service did not accept; an uncertain write is never
// resent, so forwarding continues only when the new stream shows it was
// accepted.
func (inv *invocation) writeStdin(data []byte) bool {
	h := inv.current()
	backoff := minBackoff
	for {
		base := h.op.StdinOffset()
		n, err := h.op.WriteStdin(inv.b.ctx, data)
		if err == nil {
			return true
		}
		f := asFailure(err)
		if !h.s.ended() && refused(f) {
			data = data[n:] // the offset already counts the accepted n
			if !inv.sleep(backoff) {
				return false
			}
			backoff = min(2*backoff, maxBackoff)
			continue
		}
		if !h.s.ended() {
			if f.Code != sp.CodeStdinClosed && f.Code != sp.CodeNotRunning && f.Code != sp.CodeReleased {
				inv.log.Warn("stdin forwarding stopped", "error", err)
			}
			return false
		}
		var ok bool
		if h, ok = inv.relink(h); !ok {
			return false
		}
		got := h.op.StdinOffset()
		switch {
		case f.Effect == sandboxwire.EffectPossible && got == base+uint64(len(data)):
			return true
		case f.Effect == sandboxwire.EffectPossible || got != base+uint64(n):
			inv.log.Warn("stdin forwarding stopped after an uncertain write", "offset", got)
			return false
		}
		data = data[n:]
	}
}

func (inv *invocation) closeStdin() {
	inv.request(inv.current(), true, func(h handle) error { return h.op.CloseStdin(inv.b.ctx) })
}

func (inv *invocation) exitDecided() bool {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	return inv.exited
}

// ackLoop acknowledges each delivered prefix, letting the service reclaim
// its replay and keep reading output.
func (inv *invocation) ackLoop() {
	defer inv.helpers.Done()
	var acked uint64
	for {
		prefix, changed := inv.acks.state()
		if prefix > acked {
			if err := inv.request(inv.current(), true, func(h handle) error { return h.op.Ack(inv.b.ctx, prefix) }); err != nil {
				return // released, or the operation is gone
			}
			acked = prefix
			continue
		}
		select {
		case <-changed:
		case <-inv.halt:
			return
		}
	}
}

func (inv *invocation) forwardSignals() {
	defer inv.helpers.Done()
	for {
		select {
		case n := <-inv.sigs:
			inv.signal(n)
		case <-inv.halt:
			return
		}
	}
}

// signal forwards one signal the shim caught. A signal whose delivery is
// uncertain is not resent.
func (inv *invocation) signal(s processshim.Signaled) {
	h, n := inv.current(), s.Number
	if inv.term != nil && n == uint16(unix.SIGWINCH) {
		if s.Size != nil {
			size := windowSize(*s.Size)
			inv.request(h, true, func(h handle) error { return h.op.Resize(inv.b.ctx, size) })
		}
		return
	}
	target := sp.TargetInitialProcessGroup
	if inv.term != nil && slices.Contains(ptyGroupSignals, n) {
		target = sp.TargetPTYForegroundGroup
	}
	sig := sp.Signal(n)
	if f := h.s.caps.CheckSignal(sig, target); f != nil {
		inv.log.Info("signal not forwarded", "signal", n, "reason", f.Message)
		return
	}
	err := inv.request(h, false, func(h handle) error { return h.op.Signal(inv.b.ctx, sig, target) })
	if err == nil {
		return
	}
	if f := asFailure(err); f.Code != sp.CodeNotRunning && f.Code != sp.CodeReleased {
		inv.log.Info("signal not delivered", "signal", n, "error", err)
	}
}
