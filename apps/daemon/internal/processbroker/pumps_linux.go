//go:build linux

package processbroker

import (
	"slices"
	"sync"

	"golang.org/x/sys/unix"

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

// writer writes one remote stream to its descriptor. It closes its
// descriptors after the stream's last byte; on a PTY the terminal writer also
// closes descriptor 2, which the merged stream never uses.
type writer struct {
	inv    *invocation
	stream sp.Stream
	fds    []int

	mu    sync.Mutex
	queue []chunk
	wake  chan struct{}
}

func (w *writer) push(c chunk) {
	w.mu.Lock()
	w.queue = append(w.queue, c)
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *writer) next() (chunk, bool) {
	for {
		w.mu.Lock()
		if len(w.queue) > 0 {
			c := w.queue[0]
			w.queue[0] = chunk{}
			w.queue = w.queue[1:]
			w.mu.Unlock()
			return c, true
		}
		w.mu.Unlock()
		select {
		case <-w.wake:
		case <-w.inv.halt:
			return chunk{}, false
		}
	}
}

func (w *writer) run() {
	inv := w.inv
	defer inv.writing.Done()
	broken := false
	for {
		c, ok := w.next()
		if !ok {
			return
		}
		switch {
		case c.close:
			for _, i := range w.fds {
				inv.closeFD(i)
			}
			inv.acks.deliver(c.seq)
			return
		case !broken:
			err := writeFD(inv.endpoint(w.fds[0]), c.data, inv.abort)
			if err == errStopped {
				return
			}
			if err != nil {
				// The reader is gone; the remote writer gets EPIPE as it
				// would locally. The chunk is delivered at once: closing
				// the remote output may wait for a new stream, which the
				// operation's settlement may in turn wait behind.
				broken = true
				inv.log.Info("output reader gone", "stream", w.stream, "error", err)
				inv.helpers.Add(1)
				go func() {
					defer inv.helpers.Done()
					inv.closeOutput(w.stream)
				}()
			}
		}
		inv.acks.deliver(c.seq)
	}
}

func (inv *invocation) closeOutput(stream sp.Stream) {
	h := inv.current()
	for {
		err := h.op.CloseOutput(inv.b.ctx, stream)
		if err == nil || !h.s.ended() {
			return
		}
		var ok bool
		if h, ok = inv.relink(h); !ok {
			return
		}
	}
}

// pumpStdin forwards descriptor 0 until end of file, the exit or the shim's
// loss. It owns descriptor 0 and stopIn and closes both when it returns, so
// teardown never waits for a stdin request still on the stream.
func (inv *invocation) pumpStdin(caps sp.Capabilities) {
	ep := inv.endpoint(0)
	defer func() {
		inv.closeFD(0)
		inv.stopIn.close()
	}()
	buf := make([]byte, min(int(caps.MaxDataBytes), sandboxwire.MaxPayload))
	for {
		n, err := readFD(ep, buf, inv.stopIn)
		switch {
		case err == errStopped:
			// After the exit, remote background readers see end of file
			// rather than wait for input the Harness no longer sends.
			if inv.term == nil && inv.exitDecided() {
				inv.current().op.CloseStdin(inv.b.ctx)
			}
			return
		case n == 0 || err != nil:
			if inv.term == nil {
				inv.closeStdin()
			}
			return
		}
		if !inv.writeStdin(buf[:n]) {
			return
		}
	}
}

// writeStdin writes data at the tracked offset. A write that had no effect
// continues on the next stream; an uncertain one is never resent, so
// forwarding continues only when the new stream shows it was accepted.
func (inv *invocation) writeStdin(data []byte) bool {
	h := inv.current()
	for {
		base := h.op.StdinOffset()
		n, err := h.op.WriteStdin(inv.b.ctx, data)
		if err == nil {
			return true
		}
		f := asFailure(err)
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
	h := inv.current()
	for {
		err := h.op.CloseStdin(inv.b.ctx)
		if err == nil || !h.s.ended() {
			return
		}
		var ok bool
		if h, ok = inv.relink(h); !ok {
			return
		}
	}
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
			h := inv.current()
			err := h.op.Ack(inv.b.ctx, prefix)
			switch {
			case err == nil:
				acked = prefix
				continue
			case h.s.ended():
				if _, ok := inv.relink(h); !ok {
					return
				}
				continue
			default:
				return // released, or the operation is gone
			}
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
func (inv *invocation) signal(n uint16) {
	h := inv.current()
	if inv.term != nil && n == uint16(unix.SIGWINCH) {
		size := inv.term.size()
		for {
			err := h.op.Resize(inv.b.ctx, size)
			if err == nil || !h.s.ended() {
				return
			}
			var ok bool
			if h, ok = inv.relink(h); !ok {
				return
			}
		}
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
	for {
		err := h.op.Signal(inv.b.ctx, sig, target)
		if err == nil {
			return
		}
		f := asFailure(err)
		if !h.s.ended() || f.Effect != sandboxwire.EffectNone {
			if f.Code != sp.CodeNotRunning && f.Code != sp.CodeReleased {
				inv.log.Info("signal not delivered", "signal", n, "error", err)
			}
			return
		}
		var ok bool
		if h, ok = inv.relink(h); !ok {
			return
		}
	}
}
