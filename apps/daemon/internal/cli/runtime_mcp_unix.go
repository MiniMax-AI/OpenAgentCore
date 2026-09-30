//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

func execRuntimeMCP(invocation mcpInvocation) error {
	if err := configureMCPProcess(invocation); err != nil {
		return err
	}
	command, err := exec.LookPath(invocation.command)
	if err != nil {
		return errRuntimeMCP
	}
	if syscall.Exec(command, invocation.args, invocation.env) != nil {
		return errRuntimeMCP
	}
	return nil
}
