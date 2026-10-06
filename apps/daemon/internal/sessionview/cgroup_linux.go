//go:build linux

package sessionview

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// cgroupPoll is how often cgroup.events is read while a cgroup's processes end.
const cgroupPoll = 10 * time.Millisecond

// errPopulated is a cgroup whose processes did not all end in time.
var errPopulated = errors.New("processes still run in the cgroup")

// CheckCgroups checks, without creating anything, that parent is a directory of a cgroup v2 hierarchy and that this process runs neither in parent nor in a cgroup below it, where Recover would end it. It returns ErrCgroup naming the requirement that failed.
func CheckCgroups(parent string) error {
	if !filepath.IsAbs(parent) {
		return &Error{Kind: ErrCgroup, Op: "check", Path: parent, Err: errors.New("not an absolute path")}
	}
	var st unix.Statfs_t
	if err := unix.Statfs(parent, &st); err != nil {
		return &Error{Kind: ErrCgroup, Op: "statfs", Path: parent, Err: err}
	}
	if st.Type != unix.CGROUP2_SUPER_MAGIC {
		return &Error{Kind: ErrCgroup, Op: "check", Path: parent, Err: errors.New("not a cgroup v2 directory")}
	}
	if dir, err := holding(parent, os.Getpid()); err != nil {
		return &Error{Kind: ErrCgroup, Op: "check", Path: parent, Err: err}
	} else if dir != "" {
		return &Error{Kind: ErrCgroup, Op: "check", Path: dir, Err: errors.New("this process runs in the cgroup")}
	}
	return nil
}

// holding returns the cgroup in or below parent whose cgroup.procs lists pid, or "" when none does. Membership needs no translation between cgroup namespaces and mounts.
func holding(parent string, pid int) (string, error) {
	want := strconv.Itoa(pid)
	var found string
	err := filepath.WalkDir(parent, func(dir string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		procs, err := os.ReadFile(filepath.Join(dir, "cgroup.procs"))
		if err != nil {
			return err
		}
		for _, p := range strings.Fields(string(procs)) {
			if p == want {
				found = dir
				return filepath.SkipAll
			}
		}
		return nil
	})
	return found, err
}

// ProbeCgroups checks that parent can hold a view's cgroup: it creates a cgroup there, checks that it has cgroup.kill (Linux 5.14, which also brings CLONE_INTO_CGROUP from Linux 5.7) and removes it. It returns ErrCgroup naming the requirement that failed. A parent that still holds cgroups may refuse a new one, so the caller runs it after Recover.
func ProbeCgroups(parent string) error {
	dir, err := os.MkdirTemp(parent, "probe-*")
	if err != nil {
		return &Error{Kind: ErrCgroup, Op: "create", Path: parent, Err: err}
	}
	_, kerr := os.Stat(filepath.Join(dir, "cgroup.kill"))
	if err := unix.Rmdir(dir); err != nil {
		return &Error{Kind: ErrCgroup, Op: "remove", Path: dir, Err: err}
	}
	if kerr != nil {
		return &Error{Kind: ErrCgroup, Op: "check", Path: dir, Err: fmt.Errorf("no cgroup.kill: %w", kerr)}
	}
	return nil
}

// Recover ends every cgroup in parent, which the views of an earlier owner of parent left, the way a view's teardown ends its own: cgroup.kill ends its processes, cgroup.events reports it unpopulated within the teardown's bound, and it is removed. Every cgroup in parent counts as a view's, whatever its name: Recover identifies no process by name or credentials, and it never touches a process outside parent. It returns ErrCleanup for each cgroup it could not end and remove, which it keeps, and ErrCgroup when it cannot list parent. Nothing it started runs once it returns.
func Recover(parent string) error {
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
		if err := writeCgroup(dir, "cgroup.kill", "1"); err != nil {
			errs = append(errs, &Error{Kind: ErrCleanup, Op: "recover", Path: dir, Err: err})
			continue
		}
		killed = append(killed, dir)
	}
	for _, dir := range killed {
		if err := removeCgroup(dir, deadline); err != nil {
			errs = append(errs, &Error{Kind: ErrCleanup, Op: "recover", Path: dir, Err: err})
		}
	}
	return errors.Join(errs...)
}

// removeCgroup waits until no process runs in the cgroup dir, then removes it. Past deadline it keeps dir and returns errPopulated.
func removeCgroup(dir string, deadline time.Time) error {
	for {
		populated, err := isPopulated(dir)
		if err != nil {
			return err
		}
		if !populated {
			return unix.Rmdir(dir)
		}
		if time.Now().After(deadline) {
			return errPopulated
		}
		time.Sleep(cgroupPoll)
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
