//go:build windows

package daemonize

import (
	"context"
	"os"
	"os/signal"
	"sync"

	"golang.org/x/sys/windows"
)

const stopEventEnv = "OAC_RUNTIME_DAEMON_STOP_EVENT"

// This process-lifetime handle survives transitions from serving to parked.
// Windows releases it on process exit, including abrupt termination.
var backgroundStop struct {
	once   sync.Once
	handle windows.Handle
	err    error
}

func openBackgroundStop(name string) (windows.Handle, error) {
	backgroundStop.once.Do(func() {
		var stopName *uint16
		stopName, backgroundStop.err = windows.UTF16PtrFromString(name)
		if backgroundStop.err != nil {
			return
		}
		backgroundStop.handle, backgroundStop.err = windows.OpenEvent(windows.SYNCHRONIZE, false, stopName)
		if backgroundStop.err != nil {
			return
		}
		readyName, _ := windows.UTF16PtrFromString(name + "-ready")
		ready, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, readyName)
		if err != nil {
			backgroundStop.err = err
			return
		}
		backgroundStop.err = windows.SetEvent(ready)
		windows.CloseHandle(ready)
	})
	return backgroundStop.handle, backgroundStop.err
}

// NotifyContext serves both foreground Ctrl-C and an owned background stop event.
// The ready event is signalled only after the child holds its own stop handle.
func NotifyContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt)
	name := os.Getenv(stopEventEnv)
	if name == "" {
		return ctx, cancel
	}
	event, err := openBackgroundStop(name)
	if err != nil {
		cancel()
		return ctx, cancel
	}
	go func() {
		for ctx.Err() == nil {
			status, err := windows.WaitForSingleObject(event, 100)
			if err != nil || status == windows.WAIT_OBJECT_0 {
				cancel()
				return
			}
		}
	}()
	return ctx, cancel
}
