//go:build linux

package sessionview

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Recover readies parent, a canonical path, for this process's views. First, changing nothing, it checks that parent is a writable cgroup v2 directory that this process runs outside of, and that clone3, which CLONE_INTO_CGROUP needs, is available. It then ends every cgroup in parent the way a view's teardown ends its own, and last creates and removes a cgroup to check that parent can hold one with cgroup.kill, which leftover cgroups could prevent. Every cgroup in parent counts as a view's: Recover identifies no process by name or credentials. It returns ErrCgroup for a failed check and ErrCleanup for each cgroup it could not end and remove, which it keeps.
func Recover(parent string) error {
	var st unix.Statfs_t
	if err := unix.Statfs(parent, &st); err != nil {
		return &Error{Kind: ErrCgroup, Op: "statfs", Path: parent, Err: err}
	}
	if st.Type != unix.CGROUP2_SUPER_MAGIC {
		return &Error{Kind: ErrCgroup, Op: "check", Path: parent, Err: errors.New("not a cgroup v2 directory")}
	}
	if err := unix.Access(filepath.Join(parent, "cgroup.procs"), unix.W_OK); err != nil {
		return &Error{Kind: ErrCgroup, Op: "access", Path: parent, Err: err}
	}
	_, _, errno := unix.Syscall(unix.SYS_CLONE3, 0, 0, 0)
	if err := clone3Error(errno); err != nil {
		return err
	}
	// Membership in cgroup.procs needs no translation between cgroup namespaces and mounts.
	self := strconv.Itoa(os.Getpid())
	err := filepath.WalkDir(parent, func(dir string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			var procs []byte
			if procs, err = os.ReadFile(filepath.Join(dir, "cgroup.procs")); err == nil && slices.Contains(strings.Fields(string(procs)), self) {
				err = fmt.Errorf("this process runs in %s", dir)
			}
		}
		return err
	})
	if err != nil {
		return &Error{Kind: ErrCgroup, Op: "check", Path: parent, Err: err}
	}

	entries, err := os.ReadDir(parent)
	if err != nil {
		return &Error{Kind: ErrCgroup, Op: "list", Path: parent, Err: err}
	}
	deadline := time.Now().Add(closeWait)
	var killed []string
	var errs []error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(parent, e.Name())
		if err := os.WriteFile(filepath.Join(dir, "cgroup.kill"), []byte("1"), 0); err != nil {
			errs = append(errs, &Error{Kind: ErrCleanup, Op: "kill", Path: dir, Err: err})
			continue
		}
		killed = append(killed, dir)
	}
	for _, dir := range killed {
		if err := removeCgroup(dir, deadline); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	dir, err := os.MkdirTemp(parent, "check-*")
	if err != nil {
		return &Error{Kind: ErrCgroup, Op: "create", Path: parent, Err: err}
	}
	_, kerr := os.Stat(filepath.Join(dir, "cgroup.kill"))
	if err := unix.Rmdir(dir); err != nil {
		return &Error{Kind: ErrCgroup, Op: "remove", Path: dir, Err: err}
	}
	if kerr != nil {
		return &Error{Kind: ErrCgroup, Op: "check", Path: dir, Err: kerr}
	}
	return nil
}

// clone3Error reads the errno of clone3 with no arguments, which is EINVAL where clone3 is available. A kernel without it or a seccomp filter that blocks it, as Docker's default profile does without CAP_SYS_ADMIN, returns ENOSYS or EPERM.
func clone3Error(errno unix.Errno) error {
	if errno == unix.EINVAL {
		return nil
	}
	return &Error{Kind: ErrCgroup, Op: "clone3", Err: errno}
}

// removeCgroup removes the cgroup dir once no process runs in it. Past deadline it keeps dir and returns ErrCleanup.
func removeCgroup(dir string, deadline time.Time) error {
	for {
		populated, err := isPopulated(dir)
		switch {
		case err != nil:
		case !time.Now().Before(deadline):
			err = errors.New("past the teardown's bound")
		case !populated:
			err = unix.Rmdir(dir)
		default:
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if err != nil {
			return &Error{Kind: ErrCleanup, Op: "remove cgroup", Path: dir, Err: err}
		}
		return nil
	}
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
