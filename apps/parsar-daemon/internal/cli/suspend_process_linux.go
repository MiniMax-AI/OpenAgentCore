//go:build linux

package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func suspendProcessStart(pid int) (string, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", errors.New("suspend process identity unavailable")
	}
	// comm may contain whitespace and parentheses; fields after its last closing
	// parenthesis begin at the process state (field 3), starttime is field 22.
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return "", errors.New("invalid process identity")
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) <= 19 {
		return "", errors.New("invalid process identity")
	}
	return fields[19], nil
}

func signalSuspendedProcess(identity suspendIdentity) error {
	// Pin the process before reading its identity, eliminating PID reuse between
	// validation and signal delivery. A dead pidfd can never target its successor.
	fd, err := unix.PidfdOpen(identity.PID, 0)
	if err != nil {
		return errors.New("resume: daemon unavailable")
	}
	defer unix.Close(fd)
	start, err := suspendProcessStart(identity.PID)
	if err != nil || start != identity.StartTime {
		return errors.New("resume: process identity mismatch")
	}
	own, err := os.Stat("/proc/self/exe")
	if err != nil {
		return errors.New("resume: executable identity unavailable")
	}
	target, err := os.Stat(fmt.Sprintf("/proc/%d/exe", identity.PID))
	if err != nil || !os.SameFile(own, target) {
		return errors.New("resume: executable identity mismatch")
	}
	if err := unix.PidfdSendSignal(fd, unix.SIGUSR1, nil, 0); err != nil {
		return errors.New("resume: signal failed")
	}
	return nil
}
