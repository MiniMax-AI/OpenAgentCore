//go:build linux

package daemonize

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func identifyProcess(pid int) (processIdentity, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, os.ErrNotExist) {
		return processIdentity{}, ErrNotRunning
	}
	if err != nil {
		return processIdentity{}, err
	}
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return processIdentity{}, errors.New("daemonize: invalid process identity")
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" {
		return processIdentity{}, ErrNotRunning
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return processIdentity{}, err
	}
	return processIdentity{PID: pid, Start: fields[19]}, nil
}

func stopProcess(identity processIdentity, timeout time.Duration) error {
	handle, err := unix.PidfdOpen(identity.PID, 0)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(handle)
	current, err := identifyProcess(identity.PID)
	if errors.Is(err, ErrNotRunning) {
		return nil
	}
	if err != nil || current.Start != identity.Start {
		return ErrStaleOrCorrupt
	}
	if err = unix.PidfdSendSignal(handle, unix.SIGTERM, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		n, err := unix.Poll([]unix.PollFd{{Fd: int32(handle), Events: unix.POLLIN}}, 20)
		if err != nil && !errors.Is(err, unix.EINTR) {
			return err
		}
		if n > 0 {
			return nil
		}
	}
	return errors.New("daemonize: cleanup unconfirmed; process record retained")
}
