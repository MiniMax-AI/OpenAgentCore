package clirunner

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

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
}

func Start(opts StartOptions) (*Process, error) {
	if opts.Parent == nil {
		opts.Parent = context.Background()
	}
	if opts.Binary == "" {
		return nil, fmt.Errorf("clirunner: binary required")
	}
	if opts.KillTimeout <= 0 {
		opts.KillTimeout = 3 * time.Second
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

func closePipe(p io.Closer) {
	if p != nil {
		_ = p.Close()
	}
}
