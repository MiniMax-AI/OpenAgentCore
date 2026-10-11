package smolvm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/google/uuid"
	sdk "github.com/smol-machines/smolvm-sdk/smolvm-go"
)

func (p *Provider) RunCommand(ctx context.Context, ref sandbox.Reference, command sandbox.Command) (sandbox.CommandResult, error) {
	var result sandbox.CommandResult
	if err := validateCommand(command); err != nil {
		return result, err
	}
	deadline, bounded := ctx.Deadline()
	if !bounded {
		return result, sandbox.ErrInvalid
	}
	info, err := p.GetInfo(ctx, ref)
	if err != nil {
		return result, err
	}
	if !info.BootstrapComplete {
		return result, sandbox.ErrInvalid
	}
	args := command.Args
	var env []sdk.EnvVar
	var guestFile string
	if command.Stdin != nil {
		// The HTTP exec protocol carries UTF-8 stdin only. Stage byte-exact input
		// under a mode-0700 directory inside this owned VM. Never put the bytes in
		// argv, the environment, a host file or a log.
		guestFile = "/home/runtime/.oac/stdin-" + uuid.NewString()
		if _, err := p.client.UploadFile(ctx, p.name(ref), guestFile, command.Stdin); err != nil {
			// Upload may have written the file before losing its response. No
			// command was sent yet, so a separate, bounded context can erase it.
			return result, errors.Join(sandbox.ErrCommandUnconfirmed, err, p.removeStagedInput(ref, guestFile))
		}
		env = []sdk.EnvVar{{Name: "OAC_STDIN_FILE", Value: guestFile}}
		args = append([]string{"/bin/sh", "-c", `trap 'rm -f -- "$OAC_STDIN_FILE"' EXIT; "$@" < "$OAC_STDIN_FILE"`, "sh"}, args...)
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		deadlineErr := ctx.Err()
		if deadlineErr == nil {
			deadlineErr = context.DeadlineExceeded
		}
		if guestFile != "" {
			deadlineErr = errors.Join(deadlineErr, p.removeStagedInput(ref, guestFile))
		}
		return result, errors.Join(sandbox.ErrCommandUnconfirmed, deadlineErr)
	}
	seconds := int64(remaining/time.Second) + 1
	noStart := false
	// The native API returns text and base64 copies of output in one JSON body.
	// Bound that body before decoding so an oversized command cannot exhaust
	// the node's memory while enforcing the 1 MiB command-output contract.
	response, err := p.client.ExecWithResponseLimit(ctx, p.name(ref), sdk.ExecRequest{
		Command: args, Workdir: command.Directory, User: "1000:1000", AutoStart: &noStart,
		Env: env, TimeoutSecs: &seconds,
	}, 8<<20)
	if err != nil {
		return result, errors.Join(sandbox.ErrCommandUnconfirmed, err)
	}
	if response.StdoutBytes == nil || response.StderrBytes == nil {
		return result, errors.Join(sandbox.ErrCommandUnconfirmed, fmt.Errorf("native command returned no byte-exact output"))
	}
	if len(response.StdoutBytes)+len(response.StderrBytes) > 1024*1024 {
		return result, errors.Join(sandbox.ErrCommandUnconfirmed, fmt.Errorf("native command output exceeds 1 MiB"))
	}
	return sandbox.CommandResult{Stdout: string(response.StdoutBytes), Stderr: string(response.StderrBytes), ExitCode: response.ExitCode}, nil
}

// The cleanup uses fresh authority only before command execution is submitted.
// Once Exec is called, its outcome may be uncertain; the caller must reclaim
// the allocation instead of racing a still-running command for its stdin.
func (p *Provider) removeStagedInput(ref sandbox.Reference, path string) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	noStart := false
	response, err := p.client.ExecWithResponseLimit(cleanupCtx, p.name(ref), sdk.ExecRequest{
		Command: []string{"/bin/rm", "-f", "--", path}, User: "1000:1000", AutoStart: &noStart,
	}, 1<<20)
	if err != nil {
		return fmt.Errorf("remove staged command input: %w", err)
	}
	if response.ExitCode != 0 {
		return fmt.Errorf("remove staged command input: exit %d", response.ExitCode)
	}
	return nil
}
