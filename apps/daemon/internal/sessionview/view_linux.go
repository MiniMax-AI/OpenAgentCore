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
	"path/filepath"
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
	cgroup  string // the view's cgroup, once created
	pipes   [3]*os.File
	relay   *os.File // the broker's end of the relay connection

	waited     chan struct{} // closed once the launcher has been reaped
	waitErr    error
	cleanupErr error // set before done closes
	requestMu  sync.Mutex
	lastID     uint64
	requests   map[uint64]chan reply // by ID; nil once the launcher stopped answering
	spawning   chan struct{}         // held from a spawn's start until it has settled
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
	v := &View{done: make(chan struct{}), requests: map[uint64]chan reply{}, spawning: make(chan struct{}, 1)}
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
	// A fresh name in the parent makes the cgroup the view's alone.
	if v.cgroup, err = os.MkdirTemp(spec.CgroupParent, "view-*"); err != nil {
		return &Error{Kind: ErrCgroup, Op: "create", Path: spec.CgroupParent, Err: err}
	}
	cgroup, err := os.Open(v.cgroup)
	if err != nil {
		return &Error{Kind: ErrCgroup, Op: "open", Path: v.cgroup, Err: err}
	}
	defer cgroup.Close()
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
		// Cloning into the namespaces, rather than unsharing later, puts every runtime thread of the launcher in them and makes it PID 1 of the view. Cloning into the cgroup, rather than moving the launcher there, means that no process of the view ever runs outside it.
		SysProcAttr: &syscall.SysProcAttr{
			Cloneflags:  syscall.CLONE_NEWNS | syscall.CLONE_NEWNET | syscall.CLONE_NEWPID,
			Setsid:      true,
			UseCgroupFD: true,
			CgroupFD:    int(cgroup.Fd()),
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
		Command: command{Path: spec.Process.Path, Args: spec.Process.Args, Env: spec.Process.Env, Dir: spec.Process.Dir},
		UID:     spec.Process.UID, GID: spec.Process.GID, Grace: spec.Process.Grace,
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
	if err := v.ctl.send(context.Background(), message{Kind: msgProceed, Targets: targets}); err != nil {
		return v.lost("proceed", err)
	}
	m, files, err = v.ctl.recv()
	closeFiles(files)
	switch {
	case err != nil:
		return v.lost("start", err)
	case m.Kind == msgFailed:
		return m.Fail.startErr(command{Path: spec.Process.Path, Dir: spec.Process.Dir})
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

// teardown releases the view once its launcher is exiting or never started, as the package documentation describes, and disconnects the relay so that the broker stops using it. When it does not finish within closeWait, it sets cleanupErr and keeps the view's cgroup; what still runs finishes in the background. It returns the launcher's wait error and the teardown's errors.
func (v *View) teardown() (werr, err error) {
	if v.relay != nil {
		shutdown(v.relay)
	}
	deadline := time.Now().Add(closeWait)
	var errs, cleanup []error
	if v.cgroup != "" {
		if kerr := os.WriteFile(filepath.Join(v.cgroup, "cgroup.kill"), []byte("1"), 0); kerr != nil {
			cleanup = append(cleanup, &Error{Kind: ErrCleanup, Op: "kill", Path: v.cgroup, Err: kerr})
		}
	}
	waited := v.waited
	stopped := make(chan error, 1)
	go func() { stopped <- v.stopWorld() }()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
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
			cleanup = append(cleanup, &Error{Kind: ErrCleanup, Op: "teardown", Err: fmt.Errorf("%s still running after %v", strings.Join(running, " and "), closeWait)})
			waited, stopped = nil, nil
		}
	}
	if v.cgroup != "" && len(cleanup) == 0 {
		if rerr := removeCgroup(v.cgroup, deadline); rerr != nil {
			cleanup = append(cleanup, rerr)
		}
	}
	if len(cleanup) > 0 {
		v.cleanupErr = errors.Join(cleanup...)
		errs = append(errs, v.cleanupErr)
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

// reply is the launcher's answer to a request, with the process a spawn started.
type reply struct {
	message
	spawned *Spawned
}

func (v *View) watch() {
	defer close(v.done)
	var exit *Exit
	var failed error
	spawned := map[uint64]*Spawned{}
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
			if m.ID == 0 {
				exit = &m.Exit
				v.exited.Store(true)
			} else if s := spawned[m.ID]; s != nil {
				delete(spawned, m.ID)
				s.exit = m.Exit
				close(s.done)
			}
		case msgSignaled, msgSpawned:
			r := reply{message: m}
			if m.Kind == msgSpawned && m.Pid != 0 {
				// Registered before the next message, which may report its exit.
				r.spawned = &Spawned{v: v, id: m.ID, done: make(chan struct{})}
				spawned[m.ID] = r.spawned
			}
			v.requestMu.Lock()
			if replies := v.requests[m.ID]; replies != nil {
				delete(v.requests, m.ID)
				replies <- r
			}
			v.requestMu.Unlock()
		case msgFailed:
			failed = m.Fail.err()
		}
	}
	v.requestMu.Lock()
	for _, replies := range v.requests {
		close(replies)
	}
	v.requests = nil
	v.requestMu.Unlock()
	werr, terr := v.teardown()
	for _, s := range spawned {
		s.err = ErrClosed
		close(s.done)
	}
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

// errSignalExited is Signal's result once the process it signals has exited.
var errSignalExited = &Error{Kind: ErrExited, Op: "signal", Err: os.ErrProcessDone}

// Signal delivers sig to every process in the view while the process runs. Once the process has exited it delivers nothing and returns ErrExited, even while the processes it left still drain.
func (v *View) Signal(sig syscall.Signal) error {
	if v.exited.Load() {
		return errSignalExited
	}
	r, err := v.ask(message{Kind: msgSignal, Signal: sig})
	if (err == ErrClosed && v.exited.Load()) || (err == nil && !r.Delivered) {
		return errSignalExited
	}
	return err
}

// request sends m with the descriptors of files under a new ID and returns the channel its reply comes on, which closes without one once the launcher stopped answering. ctx bounds the send: when it ends first, request returns its error and m is not sent. It returns ErrClosed once the view has ended.
func (v *View) request(ctx context.Context, m message, files ...*os.File) (chan reply, error) {
	replies := make(chan reply, 1)
	v.requestMu.Lock()
	if v.requests == nil {
		v.requestMu.Unlock()
		return nil, ErrClosed
	}
	v.lastID++
	m.ID = v.lastID
	v.requests[m.ID] = replies
	v.requestMu.Unlock()
	fds := make([]int, len(files))
	for i, f := range files {
		fds[i] = int(f.Fd())
	}
	if err := v.ctl.send(ctx, m, fds...); err != nil {
		v.requestMu.Lock()
		delete(v.requests, m.ID)
		v.requestMu.Unlock()
		switch {
		case err == ctx.Err():
			return nil, err
		case errors.Is(err, syscall.EPIPE):
			// The socket is shut down: the view has ended.
			return nil, ErrClosed
		}
		return nil, &Error{Kind: ErrLauncher, Op: "request", Err: err}
	}
	return replies, nil
}

// ask sends m and returns the launcher's reply to it, or ErrClosed once the launcher stopped answering.
func (v *View) ask(m message) (reply, error) {
	replies, err := v.request(context.Background(), m)
	if err != nil {
		return reply{}, err
	}
	r, ok := <-replies
	if !ok {
		return reply{}, ErrClosed
	}
	return r, nil
}

// Spawn starts path with args, env and dir as another process in the view while the view's process runs. Its stdout and stderr are pipes, and so is its stdin when stdin is set; otherwise its stdin is /dev/null. The package documentation says how a spawned process runs and ends.
//
// A view starts one spawn at a time: Spawn waits for the one before it to settle, holding nothing. ctx bounds that wait and the start; once it ends, Spawn returns its error, and a process that starts after all is killed before the next spawn begins. Spawn returns ErrExited once the view's process has exited, ErrClosed once the view has ended, ErrExec when path did not start, and ErrLauncher when a pipe could not be made or the launcher could not be reached.
func (v *View) Spawn(ctx context.Context, path string, args, env []string, dir string, stdin bool) (*Spawned, error) {
	select {
	case v.spawning <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if v.exited.Load() {
		<-v.spawning
		return nil, &Error{Kind: ErrExited, Op: "spawn"}
	}
	// files are the launcher's: the command's read end and the process's stdio. ends are the caller's.
	files := make([]*os.File, 4)
	var cmdW *os.File
	var ends [3]*os.File
	var err error
	files[0], cmdW, err = os.Pipe()
	if err == nil && stdin {
		files[1], ends[0], err = os.Pipe()
	} else if err == nil {
		files[1], err = os.Open(os.DevNull)
	}
	for i := 1; i < 3 && err == nil; i++ {
		ends[i], files[i+1], err = os.Pipe()
	}
	if err != nil {
		closeFiles(append(files, cmdW))
		closeFiles(ends[:])
		<-v.spawning
		return nil, &Error{Kind: ErrLauncher, Op: "pipe", Err: err}
	}
	// The command travels over a pipe, as the spec does, so its size is exec's to bound. The write ends once the launcher has read it, or once no read end remains or the spawn is cancelled.
	c := command{Path: path, Args: args, Env: env, Dir: dir}
	go func() {
		_ = gob.NewEncoder(cmdW).Encode(c)
		cmdW.Close()
	}()
	replies, err := v.request(ctx, message{Kind: msgSpawn}, files...)
	closeFiles(files)
	if err == nil {
		var r reply
		ok := false
		select {
		case r, ok = <-replies:
			replies = nil
		case <-ctx.Done():
		}
		// A context that has ended wins over a reply that came as well, and the process goes as a late one does.
		if err = ctx.Err(); err == nil {
			defer func() { <-v.spawning }()
			return started(c, r, ok, ends)
		}
		cmdW.Close()
		go func() {
			defer func() { <-v.spawning }()
			if replies != nil { // the reply is still to come
				r, ok = <-replies
			}
			if s, err := started(c, r, ok, ends); err == nil {
				s.Close()
				<-s.done
				closeFiles([]*os.File{s.Stdin, s.Stdout, s.Stderr})
			}
		}()
		return nil, err
	}
	cmdW.Close()
	closeFiles(ends[:])
	<-v.spawning
	return nil, err
}

// started returns the process a spawn of c's reply reports, with ends as its stdio, or why none started, closing ends.
func started(c command, r reply, ok bool, ends [3]*os.File) (*Spawned, error) {
	err := ErrClosed
	switch {
	case ok && r.spawned != nil:
		r.spawned.Stdin, r.spawned.Stdout, r.spawned.Stderr = ends[0], ends[1], ends[2]
		return r.spawned, nil
	case ok:
		err = r.Fail.startErr(c)
	}
	closeFiles(ends[:])
	return nil, err
}

// Spawned is a process that Spawn started. It is a clirunner.Handle.
type Spawned struct {
	// Stdin is the write end of the process's stdin pipe, or nil without one; Stdout and Stderr are the read ends of its stdout and stderr pipes. The caller owns them.
	Stdin, Stdout, Stderr *os.File

	v    *View
	id   uint64
	done chan struct{} // closed once it has been reaped or the view has ended
	exit Exit
	err  error
}

// Signal delivers sig to the process's group until the process has exited, and then returns ErrExited.
func (s *Spawned) Signal(sig syscall.Signal) error {
	r, err := s.v.ask(message{Kind: msgSignal, Spawn: s.id, Signal: sig})
	// The view's end has ended the process.
	if err == ErrClosed || (err == nil && !r.Delivered) {
		return errSignalExited
	}
	return err
}

// Wait returns the process's exit code, or -1 when a signal ended it, once it has been reaped. What remains of its process group runs on as other processes in the view do. Wait returns ErrClosed when the view ended first.
func (s *Spawned) Wait() (int, error) {
	<-s.done
	if s.exit.Signal != 0 {
		return -1, s.err
	}
	return s.exit.Code, s.err
}

// Close kills the process's group while the process runs. The view's end kills it in any case.
func (s *Spawned) Close() error {
	_ = s.Signal(syscall.SIGKILL)
	return nil
}

// Close kills the view, waits for its teardown and closes the pipes it created. It returns ErrCleanup when the teardown did not finish within its bound, and nil otherwise.
func (v *View) Close() error {
	v.closeOnce.Do(func() {
		v.closing.Store(true)
		_ = v.cmd.Process.Kill()
		// A spawn blocked on the world keeps the killed launcher, and its end, open until the teardown stops the world.
		v.ctl.interrupt()
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
