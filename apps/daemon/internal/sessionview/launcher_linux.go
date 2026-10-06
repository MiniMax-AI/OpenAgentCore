//go:build linux

package sessionview

import (
	"context"
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

	spawns chan func()    // to the restricted thread; the view sends one spawn at a time
	chld   chan os.Signal // SIGCHLD, and a wake once a spawn has registered its child
	ready  chan struct{}  // wakes the writer

	// mu orders signals, spawns and messages against the reaping. running holds from the process's start until it is reaped, and code is how it exited; termAt is when TERM first went to the view. spawned maps the pid of the process and of each spawned process to its ID until the pid is reaped; forking holds while a spawn starts a child it has not registered yet. out holds the messages the writer sends next.
	mu      sync.Mutex
	running bool
	code    int
	termAt  time.Time
	spawned map[int]uint64
	forking bool
	out     []message
}

func runLauncher() int {
	ctl, err := newControl(os.NewFile(controlFD, "sessionview-control"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "sessionview launcher: %v\n", err)
		return 1
	}
	l := &launcher{ctl: ctl, proceed: make(chan struct{}), spawns: make(chan func(), 1), chld: make(chan os.Signal, 1), ready: make(chan struct{}, 1), spawned: map[int]uint64{}}
	// run returns only when the build fails; once the process runs, the writer exits.
	_ = l.ctl.send(context.Background(), message{Kind: msgFailed, Fail: failureOf(l.run())})
	return 1
}

func (l *launcher) run() error {
	spec, err := readSpec()
	if err != nil {
		return err
	}
	go l.serveControl(spec)
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return mountError("make-rprivate", "/", err)
	}
	if err := loopbackUp(); err != nil {
		return &Error{Kind: ErrNetwork, Op: "loopback", Err: err}
	}
	if err := l.mountWorld(spec.Staging); err != nil {
		return err
	}
	<-l.proceed
	b := &builder{root: -1, proc: -1, listener: -1, targets: l.targets}
	defer b.close()
	if err := b.build(spec); err != nil {
		return err
	}
	l.proc = b.proc
	if err := switchRoot(b.root); err != nil {
		return err
	}
	if err := restrict(); err != nil {
		return err
	}
	if err := takeIdentity(spec); err != nil {
		return err
	}
	b.closeBuild()
	if b.listener >= 0 {
		// The launcher keeps neither the listener nor the broker connection, so the relay's end is theirs alone.
		l.relay, err = startRelay(spec, b.listener)
		b.closeListener()
		unix.Close(relayFD)
		if err != nil {
			return err
		}
	}
	if err := chdir(spec.Command.Dir); err != nil {
		return err
	}
	pid, err := startProcess(spec, spec.Command, []uintptr{stdinFD, stdoutFD, stderrFD})
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.running, l.spawned[pid] = true, 0
	l.mu.Unlock()
	for _, fd := range []int{stdinFD, stdoutFD, stderrFD} {
		unix.Close(fd)
	}
	if err := l.ctl.send(context.Background(), message{Kind: msgStarted, Pid: pid}); err != nil {
		return &Error{Kind: ErrLauncher, Op: "report start", Err: err}
	}
	l.forwardSignals()
	go l.write()
	go l.reap(spec.Grace)
	// Only this thread carries the restrictions a process inherits, so every spawn starts here.
	for {
		(<-l.spawns)()
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
			l.mu.Lock()
			l.post(message{Kind: msgSignaled, ID: m.ID, Delivered: l.signal(m.Spawn, m.Signal)})
			l.mu.Unlock()
		case msgSpawn:
			// The restricted thread takes each spawn before it forks, and the view sends the next only once the last has been answered, so this never waits.
			l.spawns <- func() { l.spawn(spec, m.ID, files) }
			continue
		}
		closeFiles(files)
	}
}

// spawn starts the command read from the first of files as the spawned process id, with the other three as its stdin, stdout and stderr, as the process's user and in a session of its own, and answers with its pid. It enters the command's directory before it forks, so a directory the world is slow to answer blocks this thread in an ordinary syscall. It forks without holding mu; while it does, the reaper leaves unregistered children, so the child's pid stays its own until it is registered.
func (l *launcher) spawn(spec *launchSpec, id uint64, files []*os.File) {
	defer closeFiles(files)
	var c command
	err := gob.NewDecoder(files[0]).Decode(&c)
	if err != nil {
		err = &Error{Kind: ErrLauncher, Op: "read spawn", Err: err}
	} else {
		err = chdir(c.Dir)
	}
	l.mu.Lock()
	if err == nil && !l.running {
		err = &Error{Kind: ErrExited, Op: "spawn"}
	}
	l.forking = err == nil
	l.mu.Unlock()
	m := message{Kind: msgSpawned, ID: id}
	if err == nil {
		m.Pid, err = startProcess(spec, c, []uintptr{files[1].Fd(), files[2].Fd(), files[3].Fd()})
	}
	l.mu.Lock()
	if err != nil {
		m.Fail = failureOf(err)
	} else {
		l.spawned[m.Pid] = id
	}
	l.forking = false
	l.post(m)
	l.mu.Unlock()
	// SIGCHLD does not queue: the child may have exited while the reaper left it.
	select {
	case l.chld <- unix.SIGCHLD:
	default:
	}
}

// post queues m for the writer, which sends the queued messages in order; a message without a kind exits the launcher once those before it are sent. mu is held.
func (l *launcher) post(m message) {
	l.out = append(l.out, m)
	select {
	case l.ready <- struct{}{}:
	default:
	}
}

// write sends what post queues, so that neither the reaper nor a signal waits on the socket.
func (l *launcher) write() {
	for range l.ready {
		l.mu.Lock()
		out := l.out
		l.out = nil
		l.mu.Unlock()
		for _, m := range out {
			if m.Kind == 0 {
				// A spawn blocked on the world keeps the launcher, and this end, from closing until the teardown stops the world, so the daemon learns of the exit from the shutdown.
				l.ctl.interrupt()
				os.Exit(l.code)
			}
			// A send that fails ends the control channel, as a receive that fails does, so that no request waits for a reply that will not come.
			if err := l.ctl.send(context.Background(), m); err != nil {
				l.ctl.interrupt()
				os.Exit(1)
			}
		}
	}
}

// signal delivers sig and reports whether it did. mu is held. With id 0 it signals every process in the view while the process runs: as PID 1 of the view, the launcher reaches them all with kill(-1) and is itself spared. Once the process has been reaped, only the drain signals what remains. Otherwise it signals the process group of the spawned process id until that is reaped; until then its pid, and so its group, cannot be reused.
func (l *launcher) signal(id uint64, sig syscall.Signal) bool {
	if id != 0 {
		for pid, sid := range l.spawned {
			if sid == id {
				return unix.Kill(-pid, sig) == nil
			}
		}
		return false
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
			l.mu.Lock()
			l.signal(0, s.(syscall.Signal))
			l.mu.Unlock()
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
	timer := time.NewTimer(grace)
	defer timer.Stop()
	// The last process other than the relay to exit is a child of the launcher by then, so its SIGCHLD ends the wait.
	for l.othersRemain() {
		select {
		case <-l.chld:
			l.reapChildren()
		case <-timer.C:
			return
		}
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

// reap reaps children as SIGCHLD reports them, starting with those that exited before it began, until the process has exited and the drain has ended, and then has the writer exit the launcher.
func (l *launcher) reap(grace time.Duration) {
	signal.Notify(l.chld, unix.SIGCHLD)
	for l.reapChildren() {
		<-l.chld
	}
	l.drain(grace)
	l.mu.Lock()
	l.post(message{})
	l.mu.Unlock()
}

// reapChildren reaps the children that have exited and reports whether the process still runs. It reaps registered children at any time and other children, the view's orphans and the relay, only while no spawn forks: until its registration, a spawned child that has exited stays a zombie and keeps its pid.
func (l *launcher) reapChildren() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	var ws unix.WaitStatus
	if l.forking {
		for pid := range l.spawned {
			if wpid, _ := unix.Wait4(pid, &ws, unix.WNOHANG, nil); wpid == pid {
				l.reaped(pid, ws)
			}
		}
		return l.running
	}
	for {
		pid, _ := unix.Wait4(-1, &ws, unix.WNOHANG, nil)
		if pid <= 0 {
			return l.running
		}
		l.reaped(pid, ws)
	}
}

// reaped retires the ID of pid, if it has one, and reports its exit. mu is held.
func (l *launcher) reaped(pid int, ws unix.WaitStatus) {
	id, ok := l.spawned[pid]
	if !ok {
		return
	}
	delete(l.spawned, pid)
	exit, code := exitOf(ws)
	if id == 0 {
		l.running, l.code = false, code
	}
	l.post(message{Kind: msgExited, ID: id, Exit: exit})
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
	if err := l.ctl.send(context.Background(), message{Kind: msgMounted}, dev, netns); err != nil {
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

// takeIdentity gives this thread a working directory of its own and the process's file system identity, with no capability but the two a fork needs to set the child's user, so that it enters a directory as the process would. The other threads keep theirs.
func takeIdentity(spec *launchSpec) error {
	groups := make([]int, len(spec.Groups))
	for i, g := range spec.Groups {
		groups[i] = int(g)
	}
	const setID = 1<<unix.CAP_SETUID | 1<<unix.CAP_SETGID
	caps := [2]unix.CapUserData{{Effective: setID, Permitted: setID}}
	err := unix.Unshare(unix.CLONE_FS)
	if err == nil {
		err = unix.Capset(&unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}, &caps[0])
	}
	if err == nil {
		err = unix.Setgroups(groups)
	}
	if err != nil {
		return &Error{Kind: ErrRestrict, Op: "take the process's identity", Err: err}
	}
	// With CAP_SETUID and CAP_SETGID in effect, neither fails; neither reports a failure.
	_ = unix.Setfsgid(int(spec.GID))
	_ = unix.Setfsuid(int(spec.UID))
	return nil
}

// chdir makes dir, taken from the view's root when empty or relative, this thread's working directory, which the processes it starts inherit. Signals stay blocked meanwhile: a signal to the launcher could pick this thread, and a wait on the world that a signal interrupts goes on, holding the signal back from the threads that handle it.
func chdir(dir string) error {
	if !strings.HasPrefix(dir, "/") {
		dir = "/" + dir
	}
	var all, mask unix.Sigset_t
	for i := range all.Val {
		all.Val[i] = ^all.Val[i]
	}
	_ = unix.PthreadSigmask(unix.SIG_BLOCK, &all, &mask)
	err := unix.Chdir(dir)
	_ = unix.PthreadSigmask(unix.SIG_SETMASK, &mask, nil)
	if err != nil {
		return &Error{Kind: ErrExec, Op: "chdir", Err: err}
	}
	return nil
}

// startProcess starts c as the process's user, in a session of its own and this thread's working directory, with stdio as its standard descriptors.
func startProcess(spec *launchSpec, c command, stdio []uintptr) (int, error) {
	pid, err := syscall.ForkExec(c.Path, c.Args, &syscall.ProcAttr{
		Env:   c.Env,
		Files: stdio,
		Sys: &syscall.SysProcAttr{
			Setsid:     true,
			Credential: &syscall.Credential{Uid: spec.UID, Gid: spec.GID, Groups: spec.Groups},
		},
	})
	if err != nil {
		return 0, &Error{Kind: ErrExec, Op: "exec", Err: err}
	}
	return pid, nil
}
