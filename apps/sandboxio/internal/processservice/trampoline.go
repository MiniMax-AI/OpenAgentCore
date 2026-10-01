//go:build linux

package processservice

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxwire"
)

// os/exec cannot set a child's umask, so every launch re-executes the service
// binary as a trampoline. It reads the launch from an inherited descriptor,
// applies the umask and working directory, and execs the target. The marker
// is its whole environment and argv; it is not a command-line interface.
const (
	trampolineArg0 = "oac-process-trampoline"
	trampolineEnv  = "OAC_PROCESS_TRAMPOLINE=1"
	// Descriptors the trampoline inherits: the launch, and a close-on-exec
	// status pipe that reaches EOF without data exactly when exec succeeds.
	launchFD = 3
	statusFD = 4
)

// Launch failure stages reported on the status pipe.
const (
	stageLaunch uint16 = iota + 1
	stageDescriptors
	stageChdir
	stageExec
)

// Init runs the trampoline when the process is one, and never returns then.
// Otherwise it returns at once. The service binary's main calls it first.
func Init() {
	if len(os.Args) != 1 || os.Args[0] != trampolineArg0 || len(os.Environ()) != 1 || os.Environ()[0] != trampolineEnv {
		return
	}
	runtime.LockOSThread()
	stage, err := trampoline()
	status := binary.BigEndian.AppendUint16(nil, stage)
	status = binary.BigEndian.AppendUint32(status, uint32(errnoOf(err)))
	unix.Write(statusFD, status)
	os.Exit(127)
}

func errnoOf(err error) syscall.Errno {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno
	}
	return syscall.EINVAL
}

func trampoline() (uint16, error) {
	unix.CloseOnExec(statusFD)
	var buf bytes.Buffer
	chunk := make([]byte, 64<<10)
	for {
		n, err := unix.Read(launchFD, chunk)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return stageLaunch, err
		}
		if n == 0 {
			break
		}
		buf.Write(chunk[:n])
	}
	unix.Close(launchFD)
	l, err := decodeLaunch(buf.Bytes())
	if err != nil {
		return stageLaunch, syscall.EINVAL
	}
	if err := closeInherited(); err != nil {
		return stageDescriptors, err
	}
	unix.Umask(int(l.umask))
	if err := unix.Chdir(l.cwd); err != nil {
		return stageChdir, err
	}
	// The Go runtime keeps SIGHUP and SIGINT ignored when it starts with them
	// ignored. Handling them resets both to the default at exec.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGHUP, syscall.SIGINT)
	var none unix.Sigset_t
	unix.PthreadSigmask(unix.SIG_SETMASK, &none, nil)
	return stageExec, execTarget(l)
}

// closeInherited makes exec close every descriptor from 3 up, so the target
// gets only 0, 1 and 2: fork copies any descriptor the service holds without
// FD_CLOEXEC. Closing them now instead could close the runtime's own
// descriptors under it before exec.
func closeInherited() error {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if fd, err := strconv.Atoi(e.Name()); err == nil && fd > 2 {
			unix.CloseOnExec(fd)
		}
	}
	return nil
}

// execTarget execs the executable, searching PATH for a name without '/' the
// way execvpe does.
func execTarget(l launch) error {
	if strings.Contains(l.executable, "/") {
		return unix.Exec(l.executable, l.argv, l.env)
	}
	if !l.hasPath {
		return unix.ENOENT
	}
	denied := false
	for _, dir := range strings.Split(l.path, ":") {
		if dir == "" {
			dir = "."
		}
		err := unix.Exec(dir+"/"+l.executable, l.argv, l.env)
		switch err {
		case unix.EACCES:
			denied = true
		case unix.ENOENT, unix.ENOTDIR, unix.ESTALE, unix.ENODEV, unix.ETIMEDOUT:
		default:
			return err
		}
	}
	if denied {
		return unix.EACCES
	}
	return unix.ENOENT
}

// launch is what the trampoline needs from a ProcessSpec.
type launch struct {
	executable string
	argv       []string
	env        []string
	cwd        string
	umask      uint32
	hasPath    bool
	path       string
}

func encodeLaunch(spec sandboxprocess.ProcessSpec) []byte {
	var e sandboxwire.Encoder
	e.Bytes(spec.Executable)
	e.Count(len(spec.Argv))
	for _, a := range spec.Argv {
		e.Bytes(a)
	}
	env := make([][]byte, 0, len(spec.Env)+1)
	var path []byte
	for _, v := range spec.Env {
		env = append(env, bytes.Join([][]byte{v.Name, v.Value}, []byte("=")))
		if string(v.Name) == "PATH" {
			path = v.Value
		}
	}
	if spec.PTY != nil {
		env = append(env, append([]byte("TERM="), spec.PTY.Term...))
	}
	e.Count(len(env))
	for _, v := range env {
		e.Bytes(v)
	}
	e.Bytes(spec.Cwd)
	e.U32(spec.Umask)
	e.Present(path != nil)
	if path != nil {
		e.Bytes(path)
	}
	return e.Payload()
}

func decodeLaunch(b []byte) (launch, error) {
	d := sandboxwire.NewDecoder(b)
	var l launch
	var err error
	str := func() string {
		var v []byte
		if err == nil {
			v, err = d.Bytes()
		}
		return string(v)
	}
	list := func() []string {
		var n int
		if err == nil {
			n, err = d.Count(sandboxwire.MaxPayload)
		}
		out := make([]string, n)
		for i := range out {
			out[i] = str()
		}
		return out
	}
	l.executable = str()
	l.argv = list()
	l.env = list()
	l.cwd = str()
	if err == nil {
		l.umask, err = d.U32()
	}
	if err == nil {
		l.hasPath, err = d.Present()
	}
	if l.hasPath {
		l.path = str()
	}
	if err == nil {
		err = d.Finish()
	}
	return l, err
}
