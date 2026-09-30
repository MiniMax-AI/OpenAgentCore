//go:build windows

package daemonize

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureBackground(cmd *exec.Cmd) (func(), func() error, error) {
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, nil, err
	}
	name := `Local\OpenAgentCore-daemon-` + hex.EncodeToString(nonce[:])
	stopName, _ := windows.UTF16PtrFromString(name)
	event, err := windows.CreateEvent(nil, 1, 0, stopName)
	if err != nil {
		return nil, nil, err
	}
	readyName, _ := windows.UTF16PtrFromString(name + "-ready")
	ready, err := windows.CreateEvent(nil, 1, 0, readyName)
	if err != nil {
		windows.CloseHandle(event)
		return nil, nil, err
	}
	clean := func() { windows.CloseHandle(event); windows.CloseHandle(ready) }
	filtered := cmd.Env[:0]
	for _, entry := range cmd.Env {
		if !strings.HasPrefix(entry, stopEventEnv+"=") {
			filtered = append(filtered, entry)
		}
	}
	cmd.Env = append(filtered, stopEventEnv+"="+name)
	// Detach from the console without escaping a CI or service host Job.
	// Such a host can still end the daemon when its own Job is closed.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
	return clean, func() error {
		status, err := windows.WaitForSingleObject(ready, 60000)
		if err != nil {
			return err
		}
		if status != windows.WAIT_OBJECT_0 {
			return errors.New("daemonize: background child did not establish its stop endpoint")
		}
		return nil
	}, nil
}
