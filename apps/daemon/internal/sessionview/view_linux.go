//go:build linux

package sessionview

import (
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

// launcherArg0 marks the re-executed daemon binary as a launcher.
const launcherArg0 = "oac-sessionview"

// The world mount. default_permissions stays off: the world decides access.
var (
	fuseOptions = []string{"rootmode=40000", "user_id=0", "group_id=0", "allow_other"}
	fuseFlags   = []string{"nosuid", "nodev", "noexec"}
)

const fuseMountFlags = unix.MS_NOSUID | unix.MS_NODEV | unix.MS_NOEXEC

// closeWait bounds the teardown once the launcher has exited: the world server's Stop and the end of the view's processes, which Stop may have to unblock.
var closeWait = 30 * time.Second

// exitWait bounds how long a lost launcher's exit status is awaited for an error message.
const exitWait = time.Second

// View is a running view.
type View struct {
	cmd     *exec.Cmd
	ctl     *control
	world   WorldServer
	present Presentation
	dev     *os.File
	staging string
	pipes   [3]*os.File
	relay   *os.File // the broker's end of the relay connection

	waited     chan struct{} // closed once the launcher has been reaped
	waitErr    error
	cleanupErr error // set before done closes
	signalMu   sync.Mutex
	signaled   chan bool
	exited     atomic.Bool
	closing    atomic.Bool
	closeOnce  sync.Once
	done       chan struct{}
	exit       Exit
	err        error
}

// Start builds a view for spec and starts its process. ctx bounds only the construction: once it ends, Start kills the launcher and tears the view down, which stops the world, and returns within the teardown's bound.
func Start(ctx context.Context, spec Spec) (*View, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	if err := Probe(); err != nil {
		return nil, err
	}
	v := &View{done: make(chan struct{}), signaled: make(chan bool, 1)}
	if err := v.launch(&spec); err != nil {
		return nil, v.abort(err)
	}
	stop := context.AfterFunc(ctx, func() {
		_ = v.cmd.Process.Kill()
		// A launcher blocked on a world request that the world read cannot exit, and it keeps its end of the control socket open until the world answers. The handshake stops waiting for it here; abort's teardown then stops the world, which answers the request.
		v.ctl.interrupt()
	})
	err := v.handshake(ctx, &spec)
	if !stop() {
		// The world's own error stays: it may say that the attachment must be ended.
		err = errors.Join(&Error{Kind: ErrLauncher, Op: "start", Err: ctx.Err()}, err)
	}
	if err != nil {
		return nil, v.abort(err)
	}
	go v.watch()
	return v, nil
}

func (v *View) launch(spec *Spec) error {
	child, err := v.stdio(&spec.Process)
	if err != nil {
		return &Error{Kind: ErrLauncher, Op: "pipe", Err: err}
	}
	defer func() {
		given := []*os.File{spec.Process.Stdin, spec.Process.Stdout, spec.Process.Stderr}
		for i, f := range child {
			if f != given[i] {
				f.Close()
			}
		}
	}()
	v.staging, err = os.MkdirTemp(spec.StagingParent, "oac-view-*")
	if err != nil {
		return &Error{Kind: ErrLauncher, Op: "staging", Err: err}
	}
	specR, specW, err := os.Pipe()
	if err != nil {
		return &Error{Kind: ErrLauncher, Op: "pipe", Err: err}
	}
	defer specR.Close()
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		specW.Close()
		return &Error{Kind: ErrLauncher, Op: "socketpair", Err: err}
	}
	ctlChild := os.NewFile(uintptr(pair[1]), "sessionview-control")
	defer ctlChild.Close()
	if v.ctl, err = newControl(os.NewFile(uintptr(pair[0]), "sessionview-control")); err != nil {
		specW.Close()
		return &Error{Kind: ErrLauncher, Op: "control", Err: err}
	}
	files := []*os.File{specR, ctlChild, child[0], child[1], child[2]}
	if spec.Shim.declared() {
		// A stream socket, so that the broker can read it without accepting descriptors.
		pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
		if err != nil {
			specW.Close()
			return &Error{Kind: ErrLauncher, Op: "socketpair", Err: err}
		}
		v.relay = os.NewFile(uintptr(pair[0]), "sessionview-relay")
		relayChild := os.NewFile(uintptr(pair[1]), "sessionview-relay")
		defer relayChild.Close()
		files = append(files, relayChild)
	}
	v.cmd = &exec.Cmd{
		Path:       "/proc/self/exe",
		Args:       []string{launcherArg0},
		Env:        []string{},
		Stderr:     os.Stderr,
		ExtraFiles: files,
		// Cloning into the namespaces, rather than unsharing later, puts every runtime thread of the launcher in them and makes it PID 1 of the view.
		SysProcAttr: &syscall.SysProcAttr{
			Cloneflags: syscall.CLONE_NEWNS | syscall.CLONE_NEWNET | syscall.CLONE_NEWPID,
			Setsid:     true,
		},
	}
	if err := v.cmd.Start(); err != nil {
		specW.Close()
		v.cmd = nil
		return &Error{Kind: ErrLauncher, Op: "start", Err: err}
	}
	v.waited = make(chan struct{})
	go func() {
		v.waitErr = v.cmd.Wait()
		close(v.waited)
	}()
	ls := &launchSpec{
		Staging: v.staging, Private: spec.Private, Overlays: spec.Overlays, Shim: spec.Shim,
		Path: spec.Process.Path, Args: spec.Process.Args, Env: spec.Process.Env, Dir: spec.Process.Dir,
		UID: spec.Process.UID, GID: spec.Process.GID, Groups: spec.Process.Groups, Grace: spec.Process.Grace,
	}
	// A launcher that dies early breaks the pipe; the handshake reports that.
	go func() {
		_ = gob.NewEncoder(specW).Encode(ls)
		specW.Close()
	}()
	return nil
}

// stdio returns the launcher's ends of the process's stdio, creating pipes where the spec gives no file. The pipes belong to the process's user, so that the relay can open them as its own.
func (v *View) stdio(p *Process) ([3]*os.File, error) {
	child := [3]*os.File{p.Stdin, p.Stdout, p.Stderr}
	for i := range child {
		if child[i] != nil {
			continue
		}
		r, w, err := os.Pipe()
		if err == nil {
			if err = r.Chown(int(p.UID), int(p.GID)); err != nil {
				r.Close()
				w.Close()
			}
		}
		if err != nil {
			for j := range i {
				if v.pipes[j] != nil {
					v.pipes[j].Close()
					child[j].Close()
				}
			}
			return child, err
		}
		if i == 0 {
			child[i], v.pipes[i] = r, w
		} else {
			child[i], v.pipes[i] = w, r
		}
	}
	return child, nil
}

func (v *View) handshake(ctx context.Context, spec *Spec) error {
	m, files, err := v.ctl.recv()
	if err != nil {
		return v.lost("mount", err)
	}
	if m.Kind == msgFailed {
		return m.Fail.err()
	}
	if m.Kind != msgMounted || len(files) != 2 {
		closeFiles(files)
		return &Error{Kind: ErrLauncher, Op: "mount", Err: fmt.Errorf("unexpected message %d with %d files", m.Kind, len(files))}
	}
	v.dev = files[0]
	netns := files[1]
	defer netns.Close()
	mps := spec.mountpoints()
	world, present, err := spec.World(ctx, v.dev, WorldMount{Options: fuseOptions, Flags: fuseFlags, UID: spec.Process.UID, GID: spec.Process.GID, Mountpoints: mps})
	if err != nil {
		return &Error{Kind: ErrWorld, Op: "serve", Err: err}
	}
	v.world, v.present = world, present
	targets, err := targetsOf(mps, present)
	if err != nil {
		return &Error{Kind: ErrWorld, Op: "present", Err: err}
	}
	if spec.Network.Setup != nil {
		if err := spec.Network.Setup(netns); err != nil {
			return &Error{Kind: ErrNetwork, Op: "setup", Err: err}
		}
	}
	if err := v.ctl.send(message{Kind: msgProceed, Targets: targets}); err != nil {
		return v.lost("proceed", err)
	}
	m, files, err = v.ctl.recv()
	closeFiles(files)
	switch {
	case err != nil:
		return v.lost("start", err)
	case m.Kind == msgFailed:
		return m.Fail.err()
	case m.Kind != msgStarted:
		return &Error{Kind: ErrLauncher, Op: "start", Err: fmt.Errorf("unexpected message %d", m.Kind)}
	}
	return nil
}

// targetsOf pairs each mountpoint with the path the world presents it at.
func targetsOf(mps []Mountpoint, p Presentation) (map[string]string, error) {
	if len(p.Targets) != len(mps) {
		return nil, fmt.Errorf("%d targets for %d mountpoints", len(p.Targets), len(mps))
	}
	targets := make(map[string]string, len(mps))
	for i, m := range mps {
		t := p.Targets[i]
		if !isViewAbs(t) || t == "/" {
			return nil, fmt.Errorf("target %q for %s", t, m.Path)
		}
		targets[m.Path] = t
	}
	return targets, nil
}

// lost reports a launcher that stopped talking, with its exit status when it exits promptly.
func (v *View) lost(op string, err error) error {
	if errors.Is(err, io.EOF) {
		select {
		case <-v.waited:
			if v.waitErr != nil {
				err = v.waitErr
			}
		case <-time.After(exitWait):
		}
	}
	return &Error{Kind: ErrLauncher, Op: op, Err: err}
}

// abort undoes a failed Start. It returns err, joined with ErrCleanup when the teardown did not finish.
func (v *View) abort(err error) error {
	if v.cmd != nil {
		_ = v.cmd.Process.Kill()
	}
	_, _ = v.teardown()
	v.closePipes()
	if v.cleanupErr != nil {
		return errors.Join(err, v.cleanupErr)
	}
	return err
}

// teardown releases the view once its launcher is exiting or never started. It disconnects the relay, so that the broker stops using it, and stops the world server, which ends the requests still pending on the view's FUSE connection so that a process blocked on the world can exit. It waits for the launcher and the world server together up to closeWait; past that it sets cleanupErr, and what still runs finishes in the background. It returns the launcher's wait error and the teardown's errors.
func (v *View) teardown() (werr, err error) {
	if v.relay != nil {
		shutdown(v.relay)
	}
	waited := v.waited
	stopped := make(chan error, 1)
	go func() { stopped <- v.stopWorld() }()
	timer := time.NewTimer(closeWait)
	defer timer.Stop()
	var errs []error
	for waited != nil || stopped != nil {
		select {
		case <-waited:
			werr, waited = v.waitErr, nil
		case serr := <-stopped:
			errs, stopped = append(errs, serr), nil
		case <-timer.C:
			var running []string
			if waited != nil {
				running = append(running, "the view's processes")
			}
			if stopped != nil {
				running = append(running, "the world server")
			}
			v.cleanupErr = &Error{Kind: ErrCleanup, Op: "teardown", Err: fmt.Errorf("%s still running after %v", strings.Join(running, " and "), closeWait)}
			errs = append(errs, v.cleanupErr)
			waited, stopped = nil, nil
		}
	}
	if v.relay != nil {
		v.relay.Close()
	}
	if v.ctl != nil {
		v.ctl.close()
	}
	if v.staging != "" {
		os.Remove(v.staging)
	}
	return werr, errors.Join(errs...)
}

// stopWorld stops the world server, then closes the /dev/fuse connection it served.
func (v *View) stopWorld() error {
	var err error
	if v.world != nil {
		if serr := v.world.Stop(); serr != nil {
			err = &Error{Kind: ErrWorld, Op: "stop", Err: serr}
		}
	}
	if v.dev != nil {
		v.dev.Close()
	}
	return err
}

// shutdown ends both directions of a socket, whoever else holds it.
func shutdown(f *os.File) {
	if c, err := f.SyscallConn(); err == nil {
		c.Control(func(fd uintptr) { unix.Shutdown(int(fd), unix.SHUT_RDWR) })
	}
}

func (v *View) closePipes() {
	for _, p := range v.pipes {
		if p != nil {
			p.Close()
		}
	}
}

func (v *View) watch() {
	defer close(v.done)
	var exit *Exit
	var failed error
	for {
		m, files, err := v.ctl.recv()
		closeFiles(files)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				_ = v.cmd.Process.Kill()
			}
			break
		}
		switch m.Kind {
		case msgExited:
			exit = &m.Exit
			v.exited.Store(true)
		case msgSignaled:
			v.signaled <- m.Delivered
		case msgFailed:
			failed = m.Fail.err()
		}
	}
	werr, terr := v.teardown()
	switch {
	case exit != nil:
		v.exit = *exit
	case v.closing.Load():
		v.err = ErrClosed
	case failed != nil:
		v.err = failed
	default:
		v.err = &Error{Kind: ErrLauncher, Op: "wait", Err: werr}
	}
	if terr != nil {
		v.err = errors.Join(v.err, terr)
	}
}

// Wait returns how the process ended once the view is torn down and the world server has stopped, or once the teardown's bound expired, which it reports with ErrCleanup. It returns ErrClosed when Close ended the view first.
func (v *View) Wait() (Exit, error) {
	<-v.done
	return v.exit, v.err
}

// Presentation reports how the world presented the view's mountpoints.
func (v *View) Presentation() Presentation { return v.present }

// Signal delivers sig to every process in the view while the process runs. Once the process has exited it delivers nothing and returns ErrExited, even while the processes it left still drain.
func (v *View) Signal(sig syscall.Signal) error {
	v.signalMu.Lock()
	defer v.signalMu.Unlock()
	exited := &Error{Kind: ErrExited, Op: "signal", Err: os.ErrProcessDone}
	if v.exited.Load() {
		return exited
	}
	select {
	case <-v.done:
		return ErrClosed
	default:
	}
	if err := v.ctl.send(message{Kind: msgSignal, Signal: sig}); err != nil {
		return &Error{Kind: ErrLauncher, Op: "signal", Err: err}
	}
	select {
	case delivered := <-v.signaled:
		if !delivered {
			return exited
		}
		return nil
	case <-v.done:
		if v.exited.Load() {
			return exited
		}
		return ErrClosed
	}
}

// Close kills the view, waits for its teardown and closes the pipes it created. It returns ErrCleanup when the teardown did not finish within its bound, and nil otherwise.
func (v *View) Close() error {
	v.closeOnce.Do(func() {
		v.closing.Store(true)
		_ = v.cmd.Process.Kill()
		<-v.done
		v.closePipes()
	})
	return v.cleanupErr
}

// Relay is the broker's end of the relay's connection, or nil when the spec declares no shim. processbroker.Start takes a duplicate of it. The view shuts the connection down as it ends, so a broker still running then sees the relay lost.
func (v *View) Relay() *os.File { return v.relay }

// Stdin is the write end of the process's stdin pipe, or nil when the spec gave a file.
func (v *View) Stdin() *os.File { return v.pipes[0] }

// Stdout is the read end of the process's stdout pipe, or nil when the spec gave a file.
func (v *View) Stdout() *os.File { return v.pipes[1] }

// Stderr is the read end of the process's stderr pipe, or nil when the spec gave a file.
func (v *View) Stderr() *os.File { return v.pipes[2] }

func (s *Spec) mountpoints() []Mountpoint {
	var m []Mountpoint
	for _, d := range s.Private {
		m = append(m, Mountpoint{Path: agent.ViewPrivateRoot + "/" + d.Name, Dir: true})
	}
	m = append(m, Mountpoint{Path: agent.ViewPrivateRoot + "/" + agent.ViewShimName, Dir: true})
	if s.Shim.declared() {
		m = append(m, Mountpoint{Path: agent.ViewPrivateRoot + "/" + agent.ViewRunName, Dir: true})
	}
	m = append(m,
		Mountpoint{Path: agent.ViewProcRoot, Dir: true},
		Mountpoint{Path: agent.ViewDevRoot, Dir: true},
	)
	for _, o := range s.Overlays {
		info, err := os.Stat(o.Source)
		m = append(m, Mountpoint{Path: o.Path, Dir: err == nil && info.IsDir()})
	}
	for _, p := range s.Shim.Paths {
		m = append(m, Mountpoint{Path: p})
	}
	return m
}
