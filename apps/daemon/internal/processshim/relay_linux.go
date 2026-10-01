//go:build linux

package processshim

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"slices"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// Relaying reports whether the process runs as the relay: sessionview
// executed RelayPath with argv RelayArgs. It must run before Relay.
func Relaying() bool {
	return slices.Equal(os.Args, RelayArgs) && string(atExecFn()) == RelayPath
}

const (
	// handshakeTimeout bounds reading a shim's Request.
	handshakeTimeout = 10 * time.Second
	// relayWait bounds how long the relay, once the broker is gone, keeps
	// answering waiting shims before it exits.
	relayWait = time.Second
)

// Relay runs the Session's process relay with the broker's connection at
// RelayBrokerFD and the listening socket at RelayListenerFD, until the
// broker's connection ends. It returns the process's exit code.
func Relay() int {
	// Other processes of the Session user may not trace the relay or open
	// its descriptors through /proc.
	unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
	// The relay serves until the broker goes; signals the view forwards to
	// every process are not for it.
	signal.Ignore()
	r, err := newRelay()
	if err != nil {
		return 1
	}
	go r.accept()
	r.serveBroker()
	r.lose()
	return 0
}

// relay serves the Session's shims.
type relay struct {
	broker *net.UnixConn
	ln     *net.UnixListener
	// sendMu orders the frames to the broker. An invocation's ID is
	// allocated and its Open written under it in one step, so Open IDs reach
	// the broker in increasing order.
	sendMu sync.Mutex
	terms  terminals

	mu     sync.Mutex
	invs   map[uint64]*invocation
	lastID uint64
	gone   bool           // the broker's connection ended
	live   sync.WaitGroup // the invocations' control goroutines

	// Test seams: publishing runs between an invocation's ID and its Open,
	// and makingRaw before the control goroutine makes a terminal raw.
	publishing func(Request)
	makingRaw  func()
}

func newRelay() (*relay, error) {
	bf := os.NewFile(RelayBrokerFD, "broker")
	c, err := net.FileConn(bf)
	bf.Close()
	if err != nil {
		return nil, err
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		c.Close()
		return nil, errors.New("the broker's connection is not a Unix socket")
	}
	lf := os.NewFile(RelayListenerFD, "listener")
	l, err := net.FileListener(lf)
	lf.Close()
	if err != nil {
		uc.Close()
		return nil, err
	}
	ul, ok := l.(*net.UnixListener)
	if !ok {
		uc.Close()
		l.Close()
		return nil, errors.New("the listener is not a Unix socket")
	}
	ul.SetUnlinkOnClose(false)
	return &relay{broker: uc, ln: ul, invs: map[uint64]*invocation{}}, nil
}

func (r *relay) send(m RelayMessage) error {
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	return sandboxwire.WriteFrame(r.broker, Frame(m))
}

func (r *relay) accept() {
	for {
		c, err := r.ln.AcceptUnix()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(10 * time.Millisecond) // out of descriptors, for example
			continue
		}
		go r.handshake(c)
	}
}

// handshake reads a shim's Request and hands the invocation to the broker,
// or refuses it.
func (r *relay) handshake(c *net.UnixConn) {
	conn := NewConn(c)
	c.SetDeadline(time.Now().Add(handshakeTimeout))
	req, fds, err := conn.ReadRequest() // closes the descriptors on error
	if err != nil {
		conn.Close()
		return
	}
	inv, refusal := r.open(conn, req, fds)
	if inv == nil {
		closeAll(fds[:])
		msg := []byte(refusal)
		conn.Send(Result{Code: ExitCannotRun, Message: msg[:min(len(msg), MaxMessageBytes)]})
		conn.Close()
		return
	}
	c.SetDeadline(time.Time{})
	inv.start()
}

// open prepares, registers and publishes an invocation, or returns why it is
// refused. On refusal the caller still owns fds.
func (r *relay) open(conn *Conn, req Request, fds [3]int) (*invocation, string) {
	if req.Version != Version {
		return nil, fmt.Sprintf("IPC version %d is not %d", req.Version, Version)
	}
	inv := &invocation{r: r, conn: conn, raw: make(chan struct{}), wake: make(chan struct{}, 1)}
	var err error
	if inv.end, err = newStopFlag(); err != nil {
		return nil, err.Error()
	}
	in := &input{inv: inv, credit: make(chan uint32, 1)}
	if in.stop, err = newStopFlag(); err != nil {
		inv.end.close()
		return nil, err.Error()
	}
	inv.in = in
	var opened []endpoint
	refuse := func(msg string) (*invocation, string) {
		for _, ep := range opened {
			if ep.io >= 0 && ep.io != ep.fd {
				unix.Close(ep.io)
			}
		}
		inv.term.close()
		inv.end.close()
		in.stop.close()
		return nil, msg
	}
	for i, fd := range fds {
		ep, err := openEndpoint(fd, i > 0)
		if err != nil {
			return refuse(fmt.Sprintf("descriptor %d: %v", i, err))
		}
		opened = append(opened, ep)
		if i == 0 {
			in.ep = ep
			continue
		}
		inv.out[i] = &output{inv: inv, fd: uint8(i), ep: ep, wake: make(chan struct{}, 1), changed: make(chan struct{})}
	}
	if inv.term, err = openTerminal(&r.terms, fds[0], fds[1]); err != nil {
		return refuse(fmt.Sprintf("terminal: %v", err))
	}
	var mode *Terminal
	if inv.term != nil {
		mode = inv.term.mode()
	} else {
		inv.rawOnce.Do(func() { close(inv.raw) })
	}
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	r.mu.Lock()
	switch {
	case r.gone:
		r.mu.Unlock()
		return refuse("the process broker is unavailable")
	case len(r.invs) >= MaxInvocations:
		r.mu.Unlock()
		return refuse(fmt.Sprintf("more than %d programs are running", MaxInvocations))
	}
	r.lastID++
	inv.id = r.lastID
	r.invs[inv.id] = inv
	r.live.Add(1)
	r.mu.Unlock()
	if r.publishing != nil {
		r.publishing(req)
	}
	// The broker's messages for the ID queue until start runs the
	// invocation. A failed write means the broker is gone, which ends it.
	sandboxwire.WriteFrame(r.broker, Frame(Open{ID: inv.id, Request: req, Terminal: mode}))
	return inv, ""
}

// serveBroker dispatches the broker's messages until its connection ends.
// It never blocks on an invocation.
func (r *relay) serveBroker() {
	br := bufio.NewReaderSize(r.broker, 64<<10)
	for {
		f, err := sandboxwire.ReadFrame(br, MaxFrameBytes)
		if err != nil {
			return
		}
		m, err := DecodeBroker(f)
		if err != nil {
			return
		}
		r.mu.Lock()
		inv := r.invs[m.Invocation()]
		r.mu.Unlock()
		if inv != nil {
			inv.receive(m)
		}
	}
}

// lose ends every invocation after the broker's connection ended, and
// waits a bounded time for them.
func (r *relay) lose() {
	r.ln.Close()
	r.mu.Lock()
	r.gone = true
	invs := make([]*invocation, 0, len(r.invs))
	for _, inv := range r.invs {
		invs = append(invs, inv)
	}
	r.mu.Unlock()
	for _, inv := range invs {
		inv.end.set()
		inv.control(lost{})
	}
	done := make(chan struct{})
	go func() {
		r.live.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(relayWait):
	}
}

// invocation is one shim's invocation in the relay.
type invocation struct {
	r    *relay
	id   uint64
	conn *Conn
	term *terminal // nil for pipes
	in   *input
	out  [3]*output // 1 and 2
	// end is set by End or the broker's loss; it stops every pump and wait.
	end *stopFlag
	// raw closes once stdin may be read and output written: the terminal
	// is raw, or there is none.
	raw     chan struct{}
	rawOnce sync.Once
	pumps   sync.WaitGroup // the output pumps

	ctlMu sync.Mutex
	ctl   []any
	wake  chan struct{}

	mu       sync.Mutex
	acked    bool
	answered bool // the shim has its Result or is gone
}

// lost is the control item for the broker's loss.
type lost struct{}

// start runs the published invocation.
func (inv *invocation) start() {
	go inv.run()
	inv.pumps.Add(2)
	go inv.out[1].run()
	go inv.out[2].run()
	go inv.in.run()
	go inv.readShim()
}

// receive takes one broker message without blocking.
func (inv *invocation) receive(m BrokerMessage) {
	switch m := m.(type) {
	case Output:
		inv.out[m.FD].push(item{seq: m.Seq, data: m.Data})
	case Close:
		inv.out[m.FD].push(item{seq: m.Seq, close: true})
	case Read:
		select {
		case inv.in.credit <- m.Max:
		default:
		}
	case StopInput:
		inv.in.stop.set()
	case Exit:
		inv.in.stop.set()
		inv.control(m)
	case End:
		inv.end.set()
		inv.control(m)
	default:
		inv.control(m)
	}
}

func (inv *invocation) control(m any) {
	inv.ctlMu.Lock()
	inv.ctl = append(inv.ctl, m)
	inv.ctlMu.Unlock()
	select {
	case inv.wake <- struct{}{}:
	default:
	}
}

func (inv *invocation) nextControl() any {
	for {
		inv.ctlMu.Lock()
		if len(inv.ctl) > 0 {
			m := inv.ctl[0]
			inv.ctl[0] = nil
			inv.ctl = inv.ctl[1:]
			inv.ctlMu.Unlock()
			return m
		}
		inv.ctlMu.Unlock()
		<-inv.wake
	}
}

// run handles the control messages in order until End or the broker's loss.
func (inv *invocation) run() {
	defer inv.r.live.Done()
	for {
		switch m := inv.nextControl().(type) {
		case Accept:
			inv.mu.Lock()
			inv.acked = true
			inv.mu.Unlock()
			inv.conn.Send(Ack{}) // a failure means the shim is gone; readShim reports it
		case Started:
			if inv.term != nil {
				if inv.r.makingRaw != nil {
					inv.r.makingRaw()
				}
				inv.term.makeRaw() // a terminal that stays cooked still works
			}
			inv.rawOnce.Do(func() { close(inv.raw) })
		case Exit:
			inv.exit(m)
		case Notice:
			inv.out[2].message(m.Message)
		case End:
			inv.finish("")
			return
		case lost:
			inv.finish("the process broker stopped")
			return
		}
	}
}

// exit answers the shim once the output before the exit is written. When
// End cuts that wait short, the shim gets ExitLost.
func (inv *invocation) exit(m Exit) {
	complete := inv.waitMarks(m.Marks)
	inv.term.restore()
	res := m.Result
	if !complete {
		res = Result{Code: ExitLost}
	}
	inv.mu.Lock()
	acked := inv.acked
	inv.mu.Unlock()
	if acked && len(res.Message) > 0 {
		inv.out[2].message(res.Message)
		res.Message = nil
	}
	inv.answer(res)
}

func (inv *invocation) waitMarks(marks []Mark) bool {
	for _, m := range marks {
		o := inv.out[m.FD]
		for {
			done, changed := o.reached(m.Seq)
			if done {
				break
			}
			select {
			case <-changed:
			case <-inv.end.c:
				if done, _ := o.reached(m.Seq); !done {
					return false
				}
			}
		}
	}
	return true
}

// answer sends the shim its one Result, unless it has one or is gone.
func (inv *invocation) answer(r Result) {
	inv.mu.Lock()
	if inv.answered {
		inv.mu.Unlock()
		return
	}
	inv.answered = true
	inv.mu.Unlock()
	inv.conn.Send(r)
	inv.conn.Close()
}

// finish ends the invocation: it stops the pumps, answers a waiting shim
// with ExitLost, restores the terminal and closes every descriptor.
func (inv *invocation) finish(reason string) {
	inv.end.set()
	inv.in.stop.set()
	if reason != "" {
		inv.out[2].message([]byte(reason))
	}
	inv.answer(Result{Code: ExitLost})
	inv.term.close()
	inv.rawOnce.Do(func() { close(inv.raw) })
	inv.r.mu.Lock()
	delete(inv.r.invs, inv.id)
	inv.r.mu.Unlock()
	inv.pumps.Wait()
	inv.out[1].closeEP()
	inv.out[2].closeEP()
	inv.end.close()
}

// readShim forwards the shim's signals and reports its loss.
func (inv *invocation) readShim() {
	for {
		m, err := inv.conn.ReadMessage()
		s, ok := m.(Signal)
		if err != nil || !ok {
			inv.shimGone()
			return
		}
		sig := Signaled{ID: inv.id, Number: s.Number}
		if inv.term != nil && s.Number == uint16(unix.SIGWINCH) {
			size := inv.term.size()
			sig.Size = &size
		}
		inv.r.send(sig)
	}
}

// shimGone handles the end of the shim's connection before its Result.
func (inv *invocation) shimGone() {
	inv.mu.Lock()
	if inv.answered {
		inv.mu.Unlock()
		return
	}
	inv.answered = true
	inv.mu.Unlock()
	inv.conn.Close()
	inv.in.stop.set()
	inv.term.restore()
	inv.r.send(Gone{ID: inv.id})
}

// input reads descriptor 0 for the broker, once per Read. It owns the
// descriptor and closes it when it returns.
type input struct {
	inv    *invocation
	ep     endpoint
	credit chan uint32
	// stop is set by StopInput, Exit, the shim's loss and the end.
	stop *stopFlag
}

func (in *input) run() {
	defer in.stop.close()
	defer in.ep.close()
	select {
	case <-in.inv.raw:
	case <-in.stop.c:
		return
	}
	buf := make([]byte, sandboxwire.MaxChunk)
	for {
		var n uint32
		select {
		case n = <-in.credit:
		case <-in.stop.c:
			return
		}
		got, err := readFD(in.ep, buf[:n], in.stop)
		switch {
		case err == errStopped:
			return
		case got == 0 || err != nil:
			in.inv.r.send(InputEnd{ID: in.inv.id})
			return
		}
		if in.inv.r.send(Input{ID: in.inv.id, Data: buf[:got]}) != nil {
			return
		}
	}
}

// item is an Output or Close for an output pump. A local Close closes the
// descriptor without a report.
type item struct {
	seq   uint64
	data  []byte
	close bool
	local bool
}

// output writes one descriptor's Output in order and reports each write.
// Only its pump closes the descriptor before the invocation finishes.
type output struct {
	inv *invocation
	fd  uint8
	ep  endpoint

	epMu   sync.Mutex
	closed bool

	mu      sync.Mutex
	queue   []item
	wake    chan struct{}
	done    uint64 // the last Seq written or closed
	broken  bool   // a write failed; nothing more is reported
	changed chan struct{}
}

func (o *output) push(it item) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.queue = append(o.queue, it)
	o.mu.Unlock()
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

func (o *output) next() (item, bool) {
	for {
		o.mu.Lock()
		if len(o.queue) > 0 {
			it := o.queue[0]
			o.queue[0] = item{}
			o.queue = o.queue[1:]
			o.mu.Unlock()
			return it, true
		}
		o.mu.Unlock()
		select {
		case <-o.wake:
		case <-o.inv.end.c:
			return item{}, false
		}
	}
}

func (o *output) run() {
	defer o.inv.pumps.Done()
	id := o.inv.id
	for {
		it, ok := o.next()
		if !ok {
			return
		}
		if it.close {
			o.closeEP()
			if it.local {
				continue
			}
			if o.fd == 1 && o.inv.term != nil {
				o.inv.out[2].push(item{close: true, local: true}) // the merged stream never uses it
			}
			if o.progress(it.seq, false) {
				o.inv.r.send(Written{ID: id, FD: o.fd, Seq: it.seq})
			}
			continue
		}
		if o.isBroken() || o.isClosed() {
			continue
		}
		// Until the terminal is raw, its output processing would translate
		// the remote terminal's output a second time.
		select {
		case <-o.inv.raw:
		case <-o.inv.end.c:
			return
		}
		err := writeFD(o.ep, it.data, o.inv.end)
		switch {
		case err == errStopped:
			return
		case err != nil:
			if o.progress(it.seq, true) {
				o.inv.r.send(WriteFailed{ID: id, FD: o.fd, Seq: it.seq, Errno: errnoOf(err)})
			}
		case o.progress(it.seq, false):
			o.inv.r.send(Written{ID: id, FD: o.fd, Seq: it.seq})
		}
	}
}

// progress records seq as done, or the descriptor as broken, and reports
// whether the broker is told.
func (o *output) progress(seq uint64, failed bool) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.broken {
		return false
	}
	o.broken = failed
	o.done = max(o.done, seq)
	close(o.changed)
	o.changed = make(chan struct{})
	return true
}

// reached reports whether Output and Close through seq are done or the
// descriptor is broken, and a channel that closes on the next progress.
func (o *output) reached(seq uint64) (bool, <-chan struct{}) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.broken || o.done >= seq, o.changed
}

func (o *output) isBroken() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.broken
}

func (o *output) isClosed() bool {
	o.epMu.Lock()
	defer o.epMu.Unlock()
	return o.closed
}

func (o *output) closeEP() {
	o.epMu.Lock()
	defer o.epMu.Unlock()
	if !o.closed {
		o.closed = true
		o.ep.close()
	}
}

// message writes "oac-process-shim: msg" as far as the descriptor takes it
// now. A failed message does not break the descriptor.
func (o *output) message(msg []byte) {
	o.epMu.Lock()
	defer o.epMu.Unlock()
	if !o.closed {
		tryWrite(o.ep, fmt.Appendf(nil, "oac-process-shim: %s\n", msg))
	}
}

func errnoOf(err error) uint32 {
	var errno unix.Errno
	if errors.As(err, &errno) && errno != 0 {
		return uint32(errno)
	}
	return uint32(unix.EIO)
}
