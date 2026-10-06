//go:build linux

package processservice

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	sp "github.com/MiniMax-AI/OpenAgentCore/internal/sandboxprocess"
)

// os/exec cannot set a child's umask, so every launch re-executes the service
// binary as a trampoline. It reads the operation's Start request from an
// inherited descriptor, applies the umask and working directory, and execs the
// target. The marker is its whole environment and argv; it is not a
// command-line interface.
const (
	trampolineArg0 = "oac-process-trampoline"
	trampolineEnv  = "OAC_PROCESS_TRAMPOLINE=1"
	// Descriptors the trampoline inherits: the Start request, and a
	// close-on-exec status pipe that reaches EOF without data exactly when
	// exec succeeds.
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
	f := os.NewFile(launchFD, "launch")
	b, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return stageLaunch, err
	}
	m, err := sp.Decode(sp.OpStart, b)
	if err != nil {
		return stageLaunch, syscall.EINVAL
	}
	spec := m.(sp.StartRequest).Spec
	if err := closeInherited(); err != nil {
		return stageDescriptors, err
	}
	unix.Umask(int(spec.Umask))
	if err := unix.Chdir(string(spec.Cwd)); err != nil {
		return stageChdir, err
	}
	// The Go runtime keeps SIGHUP and SIGINT ignored when it starts with them
	// ignored. Handling them resets both to the default at exec.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGHUP, syscall.SIGINT)
	var none unix.Sigset_t
	unix.PthreadSigmask(unix.SIG_SETMASK, &none, nil)
	return stageExec, execTarget(spec)
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

// execTarget execs the spec with TERM added for a terminal, searching PATH
// for a name without '/' the way execvpe does. Decode guarantees PATH then.
func execTarget(spec sp.ProcessSpec) error {
	argv := make([]string, len(spec.Argv))
	for i, a := range spec.Argv {
		argv[i] = string(a)
	}
	var env []string
	var path string
	for _, v := range spec.Env {
		env = append(env, string(v.Name)+"="+string(v.Value))
		if string(v.Name) == "PATH" {
			path = string(v.Value)
		}
	}
	if spec.PTY != nil {
		env = append(env, "TERM="+string(spec.PTY.Term))
	}
	exe := string(spec.Executable)
	if strings.Contains(exe, "/") {
		return unix.Exec(exe, argv, env)
	}
	denied := false
	for _, dir := range strings.Split(path, ":") {
		if dir == "" {
			dir = "."
		}
		err := unix.Exec(dir+"/"+exe, argv, env)
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
