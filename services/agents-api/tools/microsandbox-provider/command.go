//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	wire "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/microsandbox"
	sdk "github.com/superradcompany/microsandbox/sdk/go"
)

func runCommand(ctx context.Context, live *sdk.Sandbox, c sandbox.Command, user string) (sandbox.CommandResult, error) {
	var result sandbox.CommandResult
	deadline, ok := ctx.Deadline()
	if !ok || wire.ValidateCommand(c) != nil {
		return result, sandbox.ErrInvalid
	}
	timeout := time.Until(deadline)
	if timeout <= 0 {
		return result, sandbox.ErrCommandUnconfirmed
	}
	options := []sdk.ExecOption{sdk.WithExecUser(user), sdk.WithExecCwd(c.Directory), sdk.WithExecTimeout(timeout), sdk.WithExecStdinPipe()}
	handle, e := live.ExecStream(ctx, c.Args[0], c.Args[1:], options...)
	if e != nil {
		return result, errors.Join(sandbox.ErrCommandUnconfirmed, e)
	}
	defer handle.Close()
	sink := handle.TakeStdin()
	if sink == nil {
		return result, sandbox.ErrCommandUnconfirmed
	}
	// Write and receive concurrently to avoid full-pipe deadlocks.
	written := make(chan error, 1)
	go func() {
		data := c.Stdin
		for len(data) > 0 {
			n := len(data)
			if n > 65536 {
				n = 65536
			}
			count, err := sink.WriteCtx(ctx, data[:n])
			if err != nil {
				written <- err
				return
			}
			if count != n {
				written <- errors.New("short command input")
				return
			}
			data = data[n:]
		}
		written <- sink.Close()
	}()
	return collectCommand(ctx, handle.Recv, written)
}

func collectCommand(ctx context.Context, receive func(context.Context) (*sdk.ExecEvent, error), written <-chan error) (sandbox.CommandResult, error) {
	var result sandbox.CommandResult
	var stdout, stderr bytes.Buffer
	exited := false
	for {
		event, err := receive(ctx)
		if err != nil {
			return result, errors.Join(sandbox.ErrCommandUnconfirmed, err)
		}
		switch event.Kind {
		case sdk.ExecEventStdout:
			if stdout.Len()+len(event.Data) > wire.MaxOutputBytes {
				return sandbox.CommandResult{}, sandbox.ErrCommandUnconfirmed
			}
			_, _ = stdout.Write(event.Data)
		case sdk.ExecEventStderr:
			if stderr.Len()+len(event.Data) > wire.MaxOutputBytes {
				return sandbox.CommandResult{}, sandbox.ErrCommandUnconfirmed
			}
			_, _ = stderr.Write(event.Data)
		case sdk.ExecEventExited:
			if exited {
				return sandbox.CommandResult{}, sandbox.ErrCommandUnconfirmed
			}
			exited = true
			result.ExitCode = event.ExitCode
		case sdk.ExecEventStdinError, sdk.ExecEventFailed:
			return sandbox.CommandResult{}, sandbox.ErrCommandUnconfirmed
		case sdk.ExecEventDone:
			if !exited {
				return sandbox.CommandResult{}, sandbox.ErrCommandUnconfirmed
			}
			select {
			case err := <-written:
				if err != nil {
					return sandbox.CommandResult{}, sandbox.ErrCommandUnconfirmed
				}
				result.Stdout, result.Stderr = stdout.String(), stderr.String()
				return result, nil
			case <-ctx.Done():
				return sandbox.CommandResult{}, sandbox.ErrCommandUnconfirmed
			}
		}
	}
}
