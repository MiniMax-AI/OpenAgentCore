//go:build windows

package cli

import (
	"os"
	"os/exec"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
)

func execRuntimeMCP(invocation mcpInvocation) error {
	if err := configureMCPProcess(invocation); err != nil {
		return err
	}
	binary, args, err := localworkspace.ResolvePackageManagerCommand(invocation.command, invocation.args[1:])
	if err != nil {
		return errRuntimeMCP
	}
	command := exec.Command(binary, args...)
	command.Dir, command.Env = invocation.cwd, invocation.env
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		return errRuntimeMCP
	}
	return nil
}
