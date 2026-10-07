package clirunner

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// DefaultKillTimeout is the KillTimeout that a zero or negative StartOptions
// or HandleOptions KillTimeout selects.
const DefaultKillTimeout = 3 * time.Second

type StartOptions struct {
	Parent      context.Context
	Binary      string
	Args        []string
	Dir         string
	Env         []string
	NeedStdin   bool
	KillTimeout time.Duration
}

type Process struct {
	Cmd    *exec.Cmd
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Stderr io.ReadCloser

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	cancelOnce    sync.Once
	cancelProcess func() error
	waitProcess   func() error

	// A handle-backed process records its exit here before closing done.
	exitCode int
	exited   bool
}

func Start(opts StartOptions) (*Process, error) {
	if opts.Parent == nil {
		opts.Parent = context.Background()
	}
	if opts.Binary == "" {
		return nil, fmt.Errorf("clirunner: binary required")
	}
	if opts.KillTimeout <= 0 {
		opts.KillTimeout = DefaultKillTimeout
	}

	// Every child owns its process group (a Job object on Windows), bounding the
	// lifetime of its descendants.
	return startProcessGroup(opts)
}

func (p *Process) Context() context.Context {
	if p == nil || p.ctx == nil {
		return context.Background()
	}
	return p.ctx
}

func (p *Process) Done() <-chan struct{} {
	if p == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return p.done
}

func (p *Process) Cancel() {
	if p == nil || p.cancelProcess == nil {
		return
	}
	p.cancelOnce.Do(func() {
		_ = p.cancelProcess()
		p.cancel()
	})
}

func (p *Process) Wait() error {
	if p == nil || p.waitProcess == nil {
		return nil
	}
	return p.waitProcess()
}

// ExitCode returns the exit code once Done is closed, or -1 when a signal ended the process. ok is false before then and when the exit is unknown.
func (p *Process) ExitCode() (code int, ok bool) {
	if p == nil {
		return 0, false
	}
	select {
	case <-p.done:
	default:
		return 0, false
	}
	if p.Cmd == nil {
		return p.exitCode, p.exited
	}
	if p.Cmd.ProcessState == nil {
		return 0, false
	}
	return p.Cmd.ProcessState.ExitCode(), true
}

func closePipe(p io.Closer) {
	if p != nil {
		_ = p.Close()
	}
}
