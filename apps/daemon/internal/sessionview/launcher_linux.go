//go:build linux

package sessionview

import (
	"encoding/gob"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/processshim"
)

// Init runs the launcher when the process was started as one, and then never returns. The daemon calls it first thing in main.
func Init() {
	if len(os.Args) != 1 || os.Args[0] != launcherArg0 {
		return
	}
	// Capability sets, no_new_privs and seccomp filters are per thread: the thread that sets them must be the one that forks the process.
	runtime.LockOSThread()
	os.Exit(runLauncher())
}

// launcher is PID 1 of the view.
type launcher struct {
	ctl         *control
	proceed     chan struct{}
	proceedOnce sync.Once
	targets     map[string]string // set before proceed closes

	proc  int // the view's /proc
	relay int // the relay's pid, or 0

	// spawns carries each spawn to the restricted thread, which runs them until reaped closes once the process has been reaped.
	spawns chan func()
	reaped chan struct{}

	// mu orders signals and spawns against the exits. running holds from the process's start until it is reaped; termAt is when TERM first went to the view; spawned holds each spawned process until it is reaped.
	mu      sync.Mutex
	running bool
	termAt  time.Time
	spawned map[int]bool
}

func runLauncher() int {
	ctl, err := newControl(os.NewFile(controlFD, "sessionview-control"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "sessionview launcher: %v\n", err)
		return 1
	}
	l := &launcher{ctl: ctl, proceed: make(chan struct{}), spawns: make(chan func()), reaped: make(chan struct{}), spawned: map[int]bool{}}
	code, err := l.run()
	if err != nil {
		_ = ctl.send(message{Kind: msgFailed, Fail: failureOf(err)})
		return 1
	}
	return code
}

func (l *launcher) run() (int, error) {
	spec, err := readSpec()
	if err != nil {
		return 0, err
	}
	go l.serveControl(spec)
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return 0, mountError("make-rprivate", "/", err)
	}
	if err := loopbackUp(); err != nil {
		return 0, &Error{Kind: ErrNetwork, Op: "loopback", Err: err}
	}
	if err := l.mountWorld(spec.Staging); err != nil {
		return 0, err
	}
	<-l.proceed
	b := &builder{root: -1, proc: -1, listener: -1, targets: l.targets}
	defer b.close()
	if err := b.build(spec); err != nil {
		return 0, err
	}
	l.proc = b.proc
	if err := switchRoot(b.root); err != nil {
		return 0, err
	}
	if err := restrict(); err != nil {
		return 0, err
	}
	b.closeBuild()
	if b.listener >= 0 {
		// The launcher keeps neither the listener nor the broker connection, so the relay's end is theirs alone.
		l.relay, err = startRelay(spec, b.listener)
		b.closeListener()
		unix.Close(relayFD)
		if err != nil {
			return 0, err
		}
	}
	pid, err := startProcess(spec, spec.Command, []uintptr{stdinFD, stdoutFD, stderrFD})
	if err != nil {
		return 0, err
	}
	l.mu.Lock()
	l.running = true
	l.mu.Unlock()
	for _, fd := range []int{stdinFD, stdoutFD, stderrFD} {
		unix.Close(fd)
	}
	if err := l.ctl.send(message{Kind: msgStarted, Pid: pid}); err != nil {
		return 0, &Error{Kind: ErrLauncher, Op: "report start", Err: err}
	}
	l.forwardSignals()
	var code int
	go func() {
		code, err = l.reap(pid)
		close(l.reaped)
	}()
	// Only this thread carries the restrictions a process inherits, so spawns start here.
	for {
		select {
		case spawn := <-l.spawns:
			spawn()
		case <-l.reaped:
			if err == nil {
				l.drain(spec.Grace)
			}
			return code, err
		}
	}
}

func readSpec() (*launchSpec, error) {
	f := os.NewFile(specFD, "sessionview-spec")
	defer f.Close()
	var s launchSpec
	if err := gob.NewDecoder(f).Decode(&s); err != nil {
		return nil, &Error{Kind: ErrLauncher, Op: "read spec", Err: err}
	}
	return &s, nil
}

// serveControl handles daemon messages. When the daemon goes away the view goes with it.
func (l *launcher) serveControl(spec *launchSpec) {
	for {
		m, files, err := l.ctl.recv()
		if err != nil {
			os.Exit(1)
		}
		switch m.Kind {
		case msgProceed:
			l.proceedOnce.Do(func() {
				l.targets = m.Targets
				close(l.proceed)
			})
		case msgSignal:
			_ = l.ctl.send(message{Kind: msgSignaled, Delivered: l.signal(m.Pid, m.Signal)})
		case msgSpawn:
			spawn := func() { l.spawn(spec, m.Command, files) }
			select {
			case l.spawns <- spawn:
			case <-l.reaped:
				// The process has been reaped, so spawn starts nothing and needs no restricted thread.
				spawn()
			}
			continue
		}
		closeFiles(files)
	}
}

// spawn starts c with files as its stdin, stdout and stderr, as the process's user and in a session of its own, and answers with its pid. Holding mu until the answer is sent keeps the report of its exit behind the answer.
func (l *launcher) spawn(spec *launchSpec, c command, files []*os.File) {
	defer closeFiles(files)
	l.mu.Lock()
	defer l.mu.Unlock()
	m := message{Kind: msgSpawned}
	err := error(&Error{Kind: ErrExited, Op: "spawn"})
	if l.running {
		m.Pid, err = startProcess(spec, c, []uintptr{files[0].Fd(), files[1].Fd(), files[2].Fd()})
	}
	if err != nil {
		m.Fail = failureOf(err)
	} else {
		l.spawned[m.Pid] = true
	}
	_ = l.ctl.send(m)
}

// signal delivers sig and reports whether it did. With pid 0 it signals every process in the view while the process runs: as PID 1 of the view, the launcher reaches them all with kill(-1) and is itself spared. Once the process has been reaped, only the drain signals what remains. Otherwise it signals the process group of the spawned process pid until that is reaped.
func (l *launcher) signal(pid int, sig syscall.Signal) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if pid != 0 {
		return l.spawned[pid] && unix.Kill(-pid, sig) == nil
	}
	if !l.running {
		return false
	}
	if sig == unix.SIGTERM && l.termAt.IsZero() {
		l.termAt = time.Now()
	}
	_ = unix.Kill(-1, sig)
	return true
}

func (l *launcher) forwardSignals() {
	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, unix.SIGHUP, unix.SIGINT, unix.SIGQUIT, unix.SIGTERM, unix.SIGUSR1, unix.SIGUSR2, unix.SIGWINCH)
	go func() {
		for s := range sigs {
			l.signal(0, s.(syscall.Signal))
		}
	}()
}

// drain gives the processes left after the process exits TERM and up to grace from that TERM to exit, reaping them, and ends once only the relay remains. When TERM already went to the view, they keep what remains of the grace and get no second TERM. The launcher's exit then kills whatever remains, the relay too.
func (l *launcher) drain(grace time.Duration) {
	l.mu.Lock()
	termAt := l.termAt
	l.mu.Unlock()
	if !termAt.IsZero() {
		grace -= time.Since(termAt)
	}
	if grace <= 0 || (termAt.IsZero() && unix.Kill(-1, unix.SIGTERM) != nil) {
		return
	}
	reaped := make(chan struct{})
	go func() {
		defer close(reaped)
		// The last process other than the relay to exit is a child of the launcher by then, so its exit ends the Wait4.
		for l.othersRemain() {
			var ws unix.WaitStatus
			pid, err := unix.Wait4(-1, &ws, 0, nil)
			if err != nil && err != unix.EINTR {
				return
			}
			if err == nil {
				l.spawnExited(pid, ws)
			}
		}
	}()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-reaped:
	case <-timer.C:
	}
}

// othersRemain reports whether a process other than the launcher and the relay remains in the view. It errs on yes.
func (l *launcher) othersRemain() bool {
	fd, err := unix.Openat(l.proc, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return true
	}
	dir := os.NewFile(uintptr(fd), "proc")
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return true
	}
	for _, n := range names {
		if pid, err := strconv.Atoi(n); err == nil && pid != 1 && pid != l.relay {
			return true
		}
	}
	return false
}

// reap collects every child, since orphans in the view reparent to PID 1, until the process exits.
func (l *launcher) reap(pid int) (int, error) {
	for {
		var ws unix.WaitStatus
		wpid, err := unix.Wait4(-1, &ws, 0, nil)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, &Error{Kind: ErrLauncher, Op: "wait", Err: err}
		}
		if wpid != pid {
			l.spawnExited(wpid, ws)
			continue
		}
		l.mu.Lock()
		l.running = false
		l.mu.Unlock()
		exit, code := exitOf(ws)
		_ = l.ctl.send(message{Kind: msgExited, Exit: exit})
		return code, nil
	}
}

// spawnExited reports the exit of the spawned process pid, which a wait collected, and kills what remains of its process group. Any other pid is one of the view's orphans.
func (l *launcher) spawnExited(pid int, ws unix.WaitStatus) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.spawned[pid] {
		return
	}
	delete(l.spawned, pid)
	_ = unix.Kill(-pid, unix.SIGKILL)
	exit, _ := exitOf(ws)
	_ = l.ctl.send(message{Kind: msgExited, Pid: pid, Exit: exit})
}

// exitOf describes how a process ended, with the code the launcher exits with for it.
func exitOf(ws unix.WaitStatus) (Exit, int) {
	if ws.Signaled() {
		return Exit{Signal: ws.Signal(), CoreDumped: ws.CoreDump()}, 128 + int(ws.Signal())
	}
	return Exit{Code: ws.ExitStatus()}, ws.ExitStatus()
}

func (l *launcher) mountWorld(staging string) error {
	dev, err := unix.Open("/dev/fuse", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return &Error{Kind: ErrNoFUSE, Op: "open", Path: "/dev/fuse", Err: err}
	}
	defer unix.Close(dev)
	opts := strings.Join(append([]string{fmt.Sprintf("fd=%d", dev)}, fuseOptions...), ",")
	if err := unix.Mount("oac-world", staging, "fuse", fuseMountFlags, opts); err != nil {
		return mountError("mount", staging, err)
	}
	netns, err := unix.Open("/proc/self/ns/net", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return &Error{Kind: ErrNetwork, Op: "open", Path: "/proc/self/ns/net", Err: err}
	}
	defer unix.Close(netns)
	if err := l.ctl.send(message{Kind: msgMounted}, dev, netns); err != nil {
		return &Error{Kind: ErrLauncher, Op: "report mount", Err: err}
	}
	return nil
}

func loopbackUp() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	ifr, err := unix.NewIfreq("lo")
	if err != nil {
		return err
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, ifr); err != nil {
		return err
	}
	ifr.SetUint16(ifr.Uint16() | unix.IFF_UP)
	return unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, ifr)
}

// startRelay starts the Session's process relay as the process's user, with the broker connection and the listening socket at the descriptors processshim gives them, no standard descriptors and an empty environment.
func startRelay(spec *launchSpec, listener int) (int, error) {
	files := make([]uintptr, max(processshim.RelayBrokerFD, processshim.RelayListenerFD)+1)
	for i := range files {
		files[i] = ^uintptr(0) // closed
	}
	files[processshim.RelayBrokerFD], files[processshim.RelayListenerFD] = relayFD, uintptr(listener)
	pid, err := syscall.ForkExec(processshim.RelayPath, processshim.RelayArgs, &syscall.ProcAttr{
		Dir:   "/",
		Env:   []string{},
		Files: files,
		Sys: &syscall.SysProcAttr{
			Setsid:     true,
			Credential: &syscall.Credential{Uid: spec.UID, Gid: spec.GID, Groups: spec.Groups},
		},
	})
	if err != nil {
		return 0, &Error{Kind: ErrExec, Op: "exec relay", Path: processshim.RelayPath, Err: err}
	}
	return pid, nil
}

// startProcess starts c as the process's user, in a session of its own, with stdio as its standard descriptors.
func startProcess(spec *launchSpec, c command, stdio []uintptr) (int, error) {
	pid, err := syscall.ForkExec(c.Path, c.Args, &syscall.ProcAttr{
		Dir:   c.Dir,
		Env:   c.Env,
		Files: stdio,
		Sys: &syscall.SysProcAttr{
			Setsid:     true,
			Credential: &syscall.Credential{Uid: spec.UID, Gid: spec.GID, Groups: spec.Groups},
		},
	})
	if err != nil {
		return 0, &Error{Kind: ErrExec, Op: "exec", Path: c.Path, Err: err}
	}
	return pid, nil
}
