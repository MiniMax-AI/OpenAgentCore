//go:build windows

package daemonize

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

func processStart(handle windows.Handle) (string, error) {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return "", err
	}
	status, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return "", err
	}
	if status == windows.WAIT_OBJECT_0 {
		return "", ErrNotRunning
	}
	return fmt.Sprintf("%d:%d", creation.HighDateTime, creation.LowDateTime), nil
}
func identifyProcess(pid int) (processIdentity, error) {
	if pid <= 0 {
		return processIdentity{}, ErrNotRunning
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return processIdentity{}, ErrNotRunning
	}
	if err != nil {
		return processIdentity{}, err
	}
	defer windows.CloseHandle(handle)
	start, err := processStart(handle)
	return processIdentity{PID: pid, Start: start}, err
}
func stopProcess(identity processIdentity, timeout time.Duration) error {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(identity.PID))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	start, err := processStart(handle)
	if errors.Is(err, ErrNotRunning) {
		return nil
	}
	if err != nil || start != identity.Start {
		return ErrStaleOrCorrupt
	}
	if identity.StopEvent == "" {
		return errors.New("daemonize: process has no owned stop endpoint")
	}
	name, err := windows.UTF16PtrFromString(identity.StopEvent)
	if err != nil {
		return err
	}
	event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	if err = windows.SetEvent(event); err != nil {
		return err
	}
	status, err := windows.WaitForSingleObject(handle, uint32(max(timeout.Milliseconds(), 1)))
	if err != nil {
		return err
	}
	if status != windows.WAIT_OBJECT_0 {
		return errors.New("daemonize: cleanup unconfirmed; process record retained")
	}
	return nil
}
