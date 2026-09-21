//go:build linux

package cli

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func execRuntimeMCP(invocation mcpInvocation) error {
	if os.Chdir(invocation.cwd) != nil {
		return errRuntimeMCP
	}
	os.Clearenv()
	for _, entry := range invocation.env {
		key, value, _ := strings.Cut(entry, "=")
		if os.Setenv(key, value) != nil {
			return errRuntimeMCP
		}
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
