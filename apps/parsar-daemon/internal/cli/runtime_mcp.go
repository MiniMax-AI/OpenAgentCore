package cli

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
)

var errRuntimeMCP = errors.New("environment MCP unavailable")

type mcpInvocation struct {
	command string
	args    []string
	cwd     string
	env     []string
}

// runRuntimeMCP executes only inside the packaged initializer's sandbox. It
// never consumes MCP stdin, opens a daemon connection or owns a child process.
func runRuntimeMCP(_ *runContext, args []string) error {
	if len(args) != 2 {
		return errRuntimeMCP
	}
	root, err := os.OpenRoot(agentcapabilities.Directory)
	if err != nil {
		return errRuntimeMCP
	}
	manifest, err := agentcapabilities.Load(root)
	root.Close()
	if err != nil {
		return errRuntimeMCP
	}
	values, err := localworkspace.ReadToolEnvironment()
	if err != nil {
		return errRuntimeMCP
	}
	invocation, err := resolveMCPInvocation(manifest, args[0], args[1], values)
	if err != nil {
		return errRuntimeMCP
	}
	return execRuntimeMCP(invocation)
}

func resolveMCPInvocation(manifest agentcapabilities.Manifest, pkg, name string, values map[string]string) (mcpInvocation, error) {
	for _, installed := range manifest.MCP {
		server := installed.Server
		if installed.PackageRoot != pkg || server.Name != name {
			continue
		}
		if server.Type != "stdio" {
			return mcpInvocation{}, errRuntimeMCP
		}
		// Defaults locate installed dependencies. Other user values require an
		// explicit env_vars declaration; the native launcher's env is never read.
		env := map[string]string{
			"PATH":       "/environment/packages/npm/bin:/environment/packages/python/bin:/usr/local/bin:/usr/bin:/bin",
			"PYTHONPATH": "/environment/packages/python",
			"HOME":       "/tmp",
			"LANG":       "C.UTF-8",
		}
		for _, key := range server.EnvVars {
			value, exists := values[key]
			if !exists || strings.ContainsRune(value, 0) {
				return mcpInvocation{}, errRuntimeMCP
			}
			env[key] = value
		}
		cwd := server.CWD
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(agentcapabilities.Directory, installed.PackageRoot, cwd)
		}
		result := mcpInvocation{command: server.Command, args: append([]string{server.Command}, server.Args...), cwd: cwd}
		for key, value := range env {
			result.env = append(result.env, key+"="+value)
		}
		sort.Strings(result.env)
		return result, nil
	}
	return mcpInvocation{}, errRuntimeMCP
}
