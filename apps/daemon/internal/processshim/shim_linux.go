//go:build linux

package processshim

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Forwarded are the signals the shim catches and reports to the relay; the
// broker forwards those the process service declares. They are every signal
// the Go runtime lets a program catch, except CHLD and PIPE, which the shim
// ignores, and URG and PROF, which the runtime uses.
var Forwarded = append([]syscall.Signal{
	unix.SIGHUP, unix.SIGINT, unix.SIGQUIT, unix.SIGABRT, unix.SIGUSR1, unix.SIGUSR2,
	unix.SIGALRM, unix.SIGTERM, unix.SIGCONT, unix.SIGTSTP, unix.SIGTTIN, unix.SIGTTOU,
	unix.SIGXCPU, unix.SIGXFSZ, unix.SIGVTALRM, unix.SIGWINCH, unix.SIGIO, unix.SIGPWR,
}, realTime(35, 64)...)

func realTime(first, last syscall.Signal) []syscall.Signal {
	var sigs []syscall.Signal
	for s := first; s <= last; s++ {
		sigs = append(sigs, s)
	}
	return sigs
}

// Run runs the shim against the relay at socketPath and returns its exit
// code, or does not return when it re-raises the remote signal.
func Run(socketPath string) int {
	caught := make(chan os.Signal, 64)
	for _, s := range Forwarded {
		// HUP or INT ignored at exec stays ignored, as in a native child.
		// The Go runtime replaces other inherited ignores.
		if !signal.Ignored(s) {
			signal.Notify(caught, s)
		}
	}
	// The Go runtime already ignores SIGURG for the program and uses it for
	// preemption, so only CHLD and PIPE are set to ignore here.
	signal.Ignore(unix.SIGCHLD, unix.SIGPIPE)

	req, err := request()
	if err != nil {
		return fail(ExitCannotRun, err.Error())
	}
	sock, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return fail(ExitCannotRun, "process relay unavailable: "+err.Error())
	}
	c := NewConn(sock)
	if err := c.SendRequest(req, [3]int{0, 1, 2}); err != nil {
		return fail(ExitCannotRun, "process relay: "+err.Error())
	}
	m, err := c.ReadMessage()
	if err != nil {
		return fail(ExitCannotRun, "process relay: "+err.Error())
	}
	switch m := m.(type) {
	case Result:
		if len(m.Message) > 0 {
			fmt.Fprintf(os.Stderr, "oac-process-shim: %s\n", m.Message)
		}
		return exit(m)
	case Ack:
	default:
		return fail(ExitCannotRun, "process relay: unexpected message")
	}

	// The relay's copies are now the only references this invocation holds,
	// so a reader of the shim's stdout sees EOF when the remote output ends.
	os.Stdin.Close()
	os.Stdout.Close()
	os.Stderr.Close()

	results := make(chan Result, 1)
	go func() {
		m, err := c.ReadMessage()
		if r, ok := m.(Result); ok && err == nil {
			results <- r
		}
		close(results)
	}()
	for {
		select {
		case s := <-caught:
			// A failed send means the relay is gone; the read reports it.
			_ = c.Send(Signal{Number: uint16(s.(syscall.Signal))})
		case r, ok := <-results:
			if !ok {
				return ExitLost
			}
			return exit(r)
		}
	}
}

func request() (Request, error) {
	cwd, err := unix.Getwd()
	if err != nil {
		return Request{}, fmt.Errorf("working directory: %w", err)
	}
	umask := unix.Umask(0)
	unix.Umask(umask)
	r := Request{
		Version:  Version,
		ExecPath: execPath(),
		Cwd:      []byte(cwd),
		Umask:    uint32(umask),
	}
	for _, a := range os.Args {
		r.Argv = append(r.Argv, []byte(a))
	}
	for _, v := range os.Environ() {
		r.Env = append(r.Env, []byte(v))
	}
	if n := len(Frame(r).Payload); n > MaxRequestBytes {
		return Request{}, fmt.Errorf("argument list and environment of %d bytes exceed %d", n, MaxRequestBytes)
	}
	return r, nil
}

// execPath returns the path the kernel executed, which keeps the directory a
// PATH search chose; argv[0] may be just the name.
func execPath() []byte {
	if p := atExecFn(); p != nil {
		return p
	}
	if len(os.Args) > 0 {
		return []byte(os.Args[0])
	}
	return nil
}

// atExecFnTag is AT_EXECFN from <linux/auxvec.h>.
const atExecFnTag = 31

func atExecFn() []byte {
	auxv, err := unix.Auxv()
	if err != nil {
		return nil
	}
	for _, kv := range auxv {
		if kv[0] != atExecFnTag {
			continue
		}
		mem, err := os.Open("/proc/self/mem")
		if err != nil {
			return nil
		}
		defer mem.Close()
		b := make([]byte, unix.PathMax)
		n, _ := mem.ReadAt(b, int64(kv[1]))
		if i := bytes.IndexByte(b[:n], 0); i > 0 {
			return b[:i]
		}
		return nil
	}
	return nil
}

func fail(code int, reason string) int {
	fmt.Fprintf(os.Stderr, "oac-process-shim: %s\n", reason)
	return code
}

// exit returns r's code, or ends the process with r's signal the way the
// remote program ended.
func exit(r Result) int {
	if r.Signal == 0 {
		return int(r.Code)
	}
	sig := syscall.Signal(r.Signal)
	runtime.LockOSThread()
	// No core file: the core would be the shim's, not the program's.
	_ = unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{})
	// Go's own handler would dump goroutines for SIGQUIT and similar, so set
	// the kernel default directly.
	var act [32]byte // struct sigaction with SIG_DFL, no flags, empty mask
	_, _, errno := unix.RawSyscall6(unix.SYS_RT_SIGACTION, uintptr(sig), uintptr(unsafe.Pointer(&act)), 0, 8, 0, 0)
	if errno == 0 || sig == unix.SIGKILL {
		var set unix.Sigset_t
		bits := uint(unsafe.Sizeof(set.Val[0])) * 8
		set.Val[(uint(sig)-1)/bits] |= 1 << ((uint(sig) - 1) % bits)
		_ = unix.PthreadSigmask(unix.SIG_UNBLOCK, &set, nil)
		_ = unix.Tgkill(unix.Getpid(), unix.Gettid(), sig)
	}
	// The signal does not end the process by default, or it could not be
	// raised; report it the way a shell does.
	return 128 + int(sig)
}
