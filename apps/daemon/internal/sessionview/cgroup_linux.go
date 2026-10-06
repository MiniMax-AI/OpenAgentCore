//go:build linux

package sessionview

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// probeArg0 marks the re-executed daemon binary as CheckCgroups's process.
const probeArg0 = "oac-sessionview-probe"

// cgroupPoll is how often cgroup.events is read while a cgroup's processes end.
const cgroupPoll = 10 * time.Millisecond

// errPopulated is a cgroup whose processes did not all end in time.
var errPopulated = errors.New("processes still run in the cgroup")

// CheckCgroups checks that parent can hold view cgroups: it is a directory of a cgroup v2 hierarchy in which this process can create a cgroup, clone a process into it with CLONE_INTO_CGROUP (Linux 5.7) and end that process with cgroup.kill (Linux 5.14). It returns ErrCgroup naming the requirement that failed. The process is the daemon binary re-executed, which Init holds until it is killed.
func CheckCgroups(parent string) error {
	if !filepath.IsAbs(parent) {
		return &Error{Kind: ErrCgroup, Op: "check", Path: parent, Err: errors.New("not an absolute path")}
	}
	var fs unix.Statfs_t
	if err := unix.Statfs(parent, &fs); err != nil {
		return &Error{Kind: ErrCgroup, Op: "statfs", Path: parent, Err: err}
	}
	if fs.Type != unix.CGROUP2_SUPER_MAGIC {
		return &Error{Kind: ErrCgroup, Op: "check", Path: parent, Err: errors.New("not a cgroup v2 directory")}
	}
	dir, err := os.MkdirTemp(parent, "probe-*")
	if err != nil {
		return &Error{Kind: ErrCgroup, Op: "create", Path: parent, Err: err}
	}
	if err := probeCgroup(dir); err != nil {
		return &Error{Kind: ErrCgroup, Op: "probe", Path: dir, Err: err}
	}
	return nil
}

// probeCgroup clones a process into the new cgroup dir, ends it with cgroup.kill and removes dir. The process waits on its stdin, so until probeCgroup closes the pipe only a signal ends it.
func probeCgroup(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "cgroup.kill")); err != nil {
		unix.Rmdir(dir)
		return fmt.Errorf("cgroup.kill: %w", err)
	}
	cgroup, err := os.Open(dir)
	if err != nil {
		unix.Rmdir(dir)
		return err
	}
	stdin, hold, err := os.Pipe()
	if err != nil {
		cgroup.Close()
		unix.Rmdir(dir)
		return err
	}
	cmd := &exec.Cmd{Path: "/proc/self/exe", Args: []string{probeArg0}, Env: []string{}, Stdin: stdin,
		SysProcAttr: &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: int(cgroup.Fd())}}
	err = cmd.Start()
	cgroup.Close()
	stdin.Close()
	if err != nil {
		hold.Close()
		unix.Rmdir(dir)
		return fmt.Errorf("clone into the cgroup: %w", err)
	}
	stop := make(chan struct{})
	timer := time.AfterFunc(closeWait, func() { close(stop) })
	kerr := endCgroup(dir, stop)
	timer.Stop()
	hold.Close()
	cmd.Wait()
	if kerr != nil {
		// The process ends with its stdin; recovery removes what remains.
		unix.Rmdir(dir)
		return fmt.Errorf("cgroup.kill: %w", kerr)
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); !ok || ws.Signal() != syscall.SIGKILL {
		return fmt.Errorf("cgroup.kill did not end the process: %v", cmd.ProcessState)
	}
	return nil
}

// Recover ends every cgroup in parent, which the views of an earlier owner of parent left, the way a view's teardown ends its own: cgroup.kill ends its processes, cgroup.events reports it unpopulated within the teardown's bound, and it is removed. Every cgroup in parent counts as a view's, whatever its name: Recover identifies no process by name or credentials, and it never touches a process outside parent. It returns ErrCleanup for each cgroup it could not end and remove, which it keeps, and ErrCgroup when it cannot list parent.
func Recover(parent string) error {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return &Error{Kind: ErrCgroup, Op: "list", Path: parent, Err: err}
	}
	stop := make(chan struct{})
	timer := time.AfterFunc(closeWait, func() { close(stop) })
	defer timer.Stop()
	results := make(chan error)
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(parent, e.Name())
		n++
		go func() {
			if err := endCgroup(dir, stop); err != nil {
				results <- &Error{Kind: ErrCleanup, Op: "recover", Path: dir, Err: err}
				return
			}
			results <- nil
		}()
	}
	var errs []error
	for range n {
		errs = append(errs, <-results)
	}
	return errors.Join(errs...)
}

// endCgroup ends every process in the cgroup dir with cgroup.kill, waits until cgroup.events reports none, then removes dir. It gives up once stop closes and keeps dir.
func endCgroup(dir string, stop <-chan struct{}) error {
	if err := writeCgroup(dir, "cgroup.kill", "1"); err != nil {
		return err
	}
	for {
		populated, err := isPopulated(dir)
		if err != nil {
			return err
		}
		if !populated {
			return unix.Rmdir(dir)
		}
		select {
		case <-stop:
			return errPopulated
		case <-time.After(cgroupPoll):
		}
	}
}

func writeCgroup(dir, name, value string) error {
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, err = f.WriteString(value)
	return errors.Join(err, f.Close())
}

// isPopulated reports whether a process runs in the cgroup dir.
func isPopulated(dir string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, "cgroup.events"))
	if err != nil {
		return false, err
	}
	for line := range strings.Lines(string(data)) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "populated "); ok {
			return v != "0", nil
		}
	}
	return false, errors.New("cgroup.events reports no populated state")
}
