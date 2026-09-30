package localworkspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ResolvePackageManagerCommand runs Windows npm/npx shims through their installed
// JavaScript entrypoints. It does not pass arguments through an extra shell.
func ResolvePackageManagerCommand(command string, args []string) (string, []string, error) {
	if runtime.GOOS != "windows" {
		return command, args, nil
	}
	name := strings.ToLower(filepath.Base(command))
	switch name {
	case "npm", "npm.cmd", "npx", "npx.cmd":
	default:
		return command, args, nil
	}
	shim, err := exec.LookPath(command)
	if err != nil {
		return "", nil, errors.New("npm or npx launcher unavailable")
	}
	if !strings.EqualFold(filepath.Ext(shim), ".cmd") {
		return shim, args, nil
	}
	name = strings.TrimSuffix(name, ".cmd")
	directory := filepath.Dir(shim)
	cli := filepath.Join(directory, "node_modules", "npm", "bin", name+"-cli.js")
	if info, err := os.Stat(cli); err != nil || !info.Mode().IsRegular() {
		return "", nil, errors.New("npm or npx CLI unavailable")
	}
	node := filepath.Join(directory, "node.exe")
	if info, err := os.Stat(node); err != nil || !info.Mode().IsRegular() {
		node, err = exec.LookPath("node")
		if err != nil {
			return "", nil, errors.New("Node.js unavailable")
		}
	}
	return node, append([]string{cli}, args...), nil
}
