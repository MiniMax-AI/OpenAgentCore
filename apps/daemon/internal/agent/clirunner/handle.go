package clirunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"syscall"
	"time"
)

// Handle is a running process that clirunner did not start, such as a Harness in an agent-host Session view. Its end also ends every descendant.
type Handle interface {
	// Signal delivers sig to the process.
	Signal(syscall.Signal) error
	// Wait returns once the process and its descendants have ended. The code is -1 when a signal ended the process. An error means the exit is unknown.
	Wait() (int, error)
	// Close kills whatever still runs and releases the handle. It is safe to call more than once and after Wait.
	Close() error
}

// HandleOptions are the stdio ends of a Handle's process and its cancellation grace.
type HandleOptions struct {
	Parent context.Context
	// Stdin is nil when the process has no stdin pipe.
	Stdin       io.WriteCloser
	Stdout      io.ReadCloser
	Stderr      io.ReadCloser
	KillTimeout time.Duration
}

// FromHandle returns a Process that owns h. Cancel, or cancelling Parent, sends TERM and closes h after KillTimeout; Wait closes Stdout, Stderr and h.
func FromHandle(h Handle, opts HandleOptions) (*Process, error) {
	if h == nil || opts.Stdout == nil || opts.Stderr == nil {
		return nil, errors.New("clirunner: handle, stdout and stderr required")
	}
	if opts.Parent == nil {
		opts.Parent = context.Background()
	}
	if opts.KillTimeout <= 0 {
		opts.KillTimeout = 3 * time.Second
	}
	ctx, cancel := context.WithCancel(opts.Parent)
	p := &Process{Stdin: opts.Stdin, Stdout: opts.Stdout, Stderr: opts.Stderr, ctx: ctx, cancel: cancel, done: make(chan struct{}), killAfter: opts.KillTimeout}
	var terminateOnce sync.Once
	p.cancelProcess = func() error {
		terminateOnce.Do(func() {
			err := h.Signal(syscall.SIGTERM)
			go func() {
				if err == nil {
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
		if err != nil {
			waitErr = fmt.Errorf("clirunner: wait: %w", err)
		} else {
			p.exitCode, p.exited = code, true
			if code != 0 {
				waitErr = fmt.Errorf("clirunner: exit code %d", code)
			}
		}
		close(p.done)
	}()
	var waitOnce sync.Once
	p.waitProcess = func() error {
		<-p.done
		waitOnce.Do(func() {
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
