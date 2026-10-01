package clirunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Handle is a running process that clirunner did not start, such as a Harness in an agent-host Session view. Its end also ends every descendant.
type Handle interface {
	// Signal delivers sig to the process and every descendant it still has. Once the process itself has exited it delivers nothing and returns an error that matches os.ErrProcessDone, even while its descendants are still ending.
	Signal(syscall.Signal) error
	// Wait returns once the process and its descendants have ended. The code is -1 when a signal ended the process. An error means the exit is unknown.
	Wait() (int, error)
	// Close kills whatever still runs and releases the handle. It never closes the stdio ends in HandleOptions, which the Process owns. It is safe to call more than once and after Wait.
	Close() error
}

// HandleOptions are the stdio ends of a Handle's process and its cancellation grace. The Process owns the stdio ends and closes them in Wait.
type HandleOptions struct {
	Parent context.Context
	// Stdin is nil when the process has no stdin pipe.
	Stdin       io.WriteCloser
	Stdout      io.ReadCloser
	Stderr      io.ReadCloser
	KillTimeout time.Duration
}

// FromHandle returns a Process that owns h. Cancel, or cancelling Parent, sends TERM and closes h after KillTimeout; cancelling a process that has ended does nothing. Wait closes the stdio ends and h, and returns the context error when the TERM reached the running process and it then exited 0, as exec.CommandContext does. A TERM that finds the process exited, which Signal reports with os.ErrProcessDone, leaves its result as it was.
func FromHandle(h Handle, opts HandleOptions) (*Process, error) {
	if h == nil || opts.Stdout == nil || opts.Stderr == nil {
		return nil, errors.New("clirunner: handle, stdout and stderr required")
	}
	if opts.Parent == nil {
		opts.Parent = context.Background()
	}
	if opts.KillTimeout <= 0 {
		opts.KillTimeout = DefaultKillTimeout
	}
	ctx, cancel := context.WithCancel(opts.Parent)
	p := &Process{Stdin: opts.Stdin, Stdout: opts.Stdout, Stderr: opts.Stderr, ctx: ctx, cancel: cancel, done: make(chan struct{}), killAfter: opts.KillTimeout}
	var terminateOnce sync.Once
	var interrupted atomic.Bool
	p.cancelProcess = func() error {
		terminateOnce.Do(func() {
			err := h.Signal(syscall.SIGTERM)
			interrupted.Store(err == nil)
			go func() {
				if err == nil || errors.Is(err, os.ErrProcessDone) {
					timer := time.NewTimer(p.killAfter)
					defer timer.Stop()
					select {
					case <-p.done:
						return
					case <-timer.C:
					}
				}
				_ = h.Close()
			}()
		})
		return nil
	}
	stop := context.AfterFunc(ctx, func() { _ = p.cancelProcess() })
	var waitErr error
	go func() {
		code, err := h.Wait()
		stop()
		// A cancel in progress settles whether it interrupted the process, and none starts after the end.
		terminateOnce.Do(func() {})
		switch {
		case err != nil:
			waitErr = fmt.Errorf("clirunner: wait: %w", err)
		case code != 0:
			waitErr = fmt.Errorf("clirunner: exit code %d", code)
		case interrupted.Load():
			// Cancel runs before the context is cancelled.
			if waitErr = ctx.Err(); waitErr == nil {
				waitErr = context.Canceled
			}
		}
		if err == nil {
			p.exitCode, p.exited = code, true
		}
		close(p.done)
	}()
	var waitOnce sync.Once
	p.waitProcess = func() error {
		<-p.done
		waitOnce.Do(func() {
			closePipe(p.Stdin)
			closePipe(p.Stdout)
			closePipe(p.Stderr)
			if err := h.Close(); err != nil {
				waitErr = errors.Join(waitErr, fmt.Errorf("clirunner: close: %w", err))
			}
		})
		return waitErr
	}
	return p, nil
}
