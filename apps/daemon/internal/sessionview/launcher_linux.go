//go:build linux

package sessionview

import (
	"encoding/gob"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
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
	started     atomic.Bool
}

func runLauncher() int {
	ctl, err := newControl(os.NewFile(controlFD, "sessionview-control"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "sessionview launcher: %v\n", err)
		return 1
	}
	l := &launcher{ctl: ctl, proceed: make(chan struct{})}
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
	go l.serveControl()
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
	b := &builder{root: -1}
	defer b.close()
	if err := b.build(spec); err != nil {
		return 0, err
	}
	if err := switchRoot(b.root); err != nil {
		return 0, err
	}
	if err := restrict(); err != nil {
		return 0, err
	}
	b.close()
	pid, err := startProcess(spec)
	if err != nil {
		return 0, err
	}
	l.started.Store(true)
	for _, fd := range []int{stdinFD, stdoutFD, stderrFD} {
		unix.Close(fd)
	}
	if err := l.ctl.send(message{Kind: msgStarted, Pid: pid}); err != nil {
		return 0, &Error{Kind: ErrLauncher, Op: "report start", Err: err}
	}
	l.forwardSignals()
	code, err := l.reap(pid)
	if err == nil {
		drain(spec.Grace)
	}
	return code, err
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
func (l *launcher) serveControl() {
	for {
		m, files, err := l.ctl.recv()
		closeFiles(files)
		if err != nil {
			os.Exit(1)
		}
		switch m.Kind {
		case msgProceed:
			l.proceedOnce.Do(func() { close(l.proceed) })
		case msgSignal:
			l.signal(m.Signal)
		}
	}
}

// signal delivers sig to every process in the view once the process has started. As PID 1 of the view, the launcher reaches them all with kill(-1) and is itself spared.
func (l *launcher) signal(sig syscall.Signal) {
	if l.started.Load() {
		_ = unix.Kill(-1, sig)
	}
}

func (l *launcher) forwardSignals() {
	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, unix.SIGHUP, unix.SIGINT, unix.SIGQUIT, unix.SIGTERM, unix.SIGUSR1, unix.SIGUSR2, unix.SIGWINCH)
	go func() {
		for s := range sigs {
			l.signal(s.(syscall.Signal))
		}
	}()
}

// drain gives the processes left after the process exits TERM and up to grace to exit, reaping them. The launcher's exit then kills whatever remains.
func drain(grace time.Duration) {
	if grace <= 0 || unix.Kill(-1, unix.SIGTERM) != nil {
		return
	}
	reaped := make(chan struct{})
	go func() {
		defer close(reaped)
		for {
			if _, err := unix.Wait4(-1, nil, 0, nil); err != nil && err != unix.EINTR {
				return
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
			continue
		}
		var exit Exit
		code := ws.ExitStatus()
		if ws.Signaled() {
			exit.Signal, exit.CoreDumped = ws.Signal(), ws.CoreDump()
			code = 128 + int(exit.Signal)
		} else {
			exit.Code = code
		}
		_ = l.ctl.send(message{Kind: msgExited, Exit: exit})
		return code, nil
	}
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

func startProcess(spec *launchSpec) (int, error) {
	pid, err := syscall.ForkExec(spec.Path, spec.Args, &syscall.ProcAttr{
		Dir:   spec.Dir,
		Env:   spec.Env,
		Files: []uintptr{stdinFD, stdoutFD, stderrFD},
		Sys: &syscall.SysProcAttr{
			Setsid:     true,
			Credential: &syscall.Credential{Uid: spec.UID, Gid: spec.GID, Groups: spec.Groups},
		},
	})
	if err != nil {
		return 0, &Error{Kind: ErrExec, Op: "exec", Path: spec.Path, Err: err}
	}
	return pid, nil
}
