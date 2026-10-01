//go:build linux

package sessionview

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// EndLeftoverViews ends every view that /proc shows. A daemon that starts views calls it at startup, before it starts any, to end the views a previous instance left; nothing else on the host may start views.
//
// A view's launcher is the process whose command line is exactly the launcher's and which is PID 1 of a PID namespace directly below /proc's. EndLeftoverViews kills each launcher, and the kernel then kills every other process in its view; the launcher exits only once its view is empty. Each launcher is pinned with a pidfd and confirmed again before the kill, so a reused pid is never signalled. It waits up to bound for every launcher to exit and returns an [ErrLauncher] error when one has not. /proc must show the caller's own PID namespace.
func EndLeftoverViews(bound time.Duration) error {
	self, err := nspid("/proc/self/status")
	if err != nil {
		return leftoverError(err)
	}
	if len(self) != 1 {
		return leftoverError(errors.New("/proc is not this process's PID namespace's"))
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return leftoverError(err)
	}
	var launchers []int
	defer func() {
		for _, fd := range launchers {
			unix.Close(fd)
		}
	}()
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		fd, err := killLauncher(pid)
		if err != nil {
			return leftoverError(err)
		}
		if fd >= 0 {
			launchers = append(launchers, fd)
		}
	}
	left, err := awaitExits(launchers, bound)
	if err != nil {
		return leftoverError(err)
	}
	if left > 0 {
		return leftoverError(fmt.Errorf("%d launchers still run after %s", left, bound))
	}
	return nil
}

func leftoverError(err error) error {
	return &Error{Kind: ErrLauncher, Op: "end leftover views", Err: err}
}

// killLauncher kills pid when it is a launcher and returns its pidfd, or -1 when pid is not a launcher or has ended.
func killLauncher(pid int) (int, error) {
	if ok, err := isLauncher(pid); err != nil || !ok {
		return -1, err
	}
	fd, err := unix.PidfdOpen(pid, 0)
	if errors.Is(err, unix.ESRCH) {
		return -1, nil
	}
	if err != nil {
		return -1, fmt.Errorf("pidfd of %d: %w", pid, err)
	}
	// What /proc shows under pid belongs to the process fd pins while that process exists, which the signal 0 confirms afterwards.
	ok, err := isLauncher(pid)
	if err == nil && ok {
		err = unix.PidfdSendSignal(fd, 0, nil, 0)
		if err == nil {
			err = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
		}
	}
	switch {
	case errors.Is(err, unix.ESRCH) || (err == nil && !ok):
		unix.Close(fd)
		return -1, nil
	case err != nil:
		unix.Close(fd)
		return -1, fmt.Errorf("kill launcher %d: %w", pid, err)
	}
	return fd, nil
}

// isLauncher reports whether pid is a launcher. A process that has ended, or a zombie, which has no command line, is not.
func isLauncher(pid int) (bool, error) {
	dir := "/proc/" + strconv.Itoa(pid) + "/"
	cmdline, err := os.ReadFile(dir + "cmdline")
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ESRCH) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if string(cmdline) != launcherArg0+"\x00" {
		return false, nil
	}
	ns, err := nspid(dir + "status")
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ESRCH) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(ns) == 2 && ns[1] == 1, nil
}

// nspid reads the NSpid line of a status file in /proc: the pid in each PID namespace, from /proc's to the process's own.
func nspid(path string) ([]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		key, value, _ := strings.Cut(sc.Text(), ":")
		if key != "NSpid" {
			continue
		}
		var ids []int
		for _, f := range strings.Fields(value) {
			id, err := strconv.Atoi(f)
			if err != nil {
				return nil, fmt.Errorf("%s: NSpid %q", path, value)
			}
			ids = append(ids, id)
		}
		if len(ids) > 0 {
			return ids, nil
		}
	}
	return nil, fmt.Errorf("%s has no NSpid", path)
}

// awaitExits waits up to bound for the process behind each pidfd to exit and returns how many have not.
func awaitExits(fds []int, bound time.Duration) (int, error) {
	deadline := time.Now().Add(bound)
	pending := fds
	for len(pending) > 0 {
		wait := time.Until(deadline)
		if wait <= 0 {
			break
		}
		polls := make([]unix.PollFd, len(pending))
		for i, fd := range pending {
			polls[i] = unix.PollFd{Fd: int32(fd), Events: unix.POLLIN}
		}
		_, err := unix.Poll(polls, int(wait.Milliseconds())+1)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return len(pending), err
		}
		var next []int
		for i, p := range polls {
			if p.Revents == 0 {
				next = append(next, pending[i])
			}
		}
		pending = next
	}
	return len(pending), nil
}
