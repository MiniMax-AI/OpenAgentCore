//go:build unix

package daemonize

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

const stopEventEnv = "OAC_RUNTIME_DAEMON_STOP_EVENT"

func NotifyContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}
