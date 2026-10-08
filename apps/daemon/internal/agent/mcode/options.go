package mcode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

type launchOptions struct {
	Dir, DataDir, Model string
	Env                 []string
	MCP                 []map[string]any
	// bindings are the effective MCP bindings rendered into MCP; native MCP
	// tool calls are observed against them.
	bindings []agent.MCPBinding
	// start runs node with script, the CLI entry, as the native process.
	start  func(clirunner.StartOptions) (*clirunner.Process, error)
	script string
	// spawn runs the Subagent history reader beside the native process, with
	// reader's node, script and data directory as the view presents them.
	spawn  func(clirunner.StartOptions) (*clirunner.Process, error)
	reader clirunner.StartOptions
	// home is the Session home on the host. It contains DataDir and belongs
	// to the Session user, so the daemon reads DataDir only within it.
	home string
}

// writeNativeConfig writes the instructions and native configuration into the
// data directory, which the native process sees at dataDir. With tools, the
// workspace bridge replaces native permissions and sandbox, and Subagents use
// it too.
func writeNativeConfig(req agent.PrepareRequest, data *os.Root, dataDir string, tools *workspaceTools) error {
	if len(req.SystemPrompt) > 32*1024 {
		return fmt.Errorf("mcode: combined instructions exceed the CLI's 32 KiB limit")
	}
	if err := data.WriteFile("AGENTS.md", []byte(req.SystemPrompt), 0o600); err != nil {
		return err
	}
	config := map[string]any{"logLevel": "error", "skills": map[string]any{"external": map[string]any{"enabled": false}}}
	config["custom_provider"] = map[string]any{"oac": modelProviderConfig(req.Prepared.Provider, req.Prepared.Model)}
	configureTextExecution(config)
	if !req.DisableSubagents {
		config["agents"] = map[string]any{"default": map[string]any{
			"tools":        []string{"task", "task_append", "task_query", "task_output", "task_stop"},
			"builtinTools": []string{"task", "task_append", "task_query", "task_output", "task_stop"}, "skills": []string{},
			"features": map[string]bool{"mavis": false, "delegation": true, "webSearch": false},
		}}
	}
	config["permissionMode"] = "auto"
	servers := map[string]any{}
	if tools != nil {
		config["permissionMode"] = "bypassPermissions"
		config["sandbox"] = map[string]bool{"enabled": false}
		if len(tools.skills) > 0 {
			// The native catalog loads Skills from its data directory, so each
			// installed Skill is linked there by name to its root as the native
			// process sees it.
			if err := data.MkdirAll("skills", 0o700); err != nil {
				return err
			}
			names := make([]string, 0, len(tools.skills))
			for _, skill := range tools.skills {
				link, target := filepath.Join("skills", skill.Metadata.Name), skill.Root()
				actual, err := data.Readlink(link)
				switch {
				case errors.Is(err, fs.ErrNotExist):
					err = data.Symlink(target, link)
				case err == nil && actual != target:
					err = errors.New("mcode: unexpected native Skill root")
				}
				if err != nil {
					return err
				}
				names = append(names, skill.Metadata.Name)
			}
			selected := config["agents"].(map[string]any)["default"].(map[string]any)
			selected["skills"] = names
			for _, key := range []string{"tools", "builtinTools"} {
				selected[key] = append(selected[key].([]string), "skill")
			}
		}
		raw, err := json.Marshal(tools.profile)
		if err != nil {
			return err
		}
		if err := data.WriteFile("workspace-profile.json", raw, 0o600); err != nil {
			return err
		}
		if !req.DisableSubagents {
			// This native data directory belongs to one public Session and its
			// descendants. ACP's ephemeral server map otherwise covers only root.
			server := tools.server(dataDir)
			servers["oac_workspace"] = map[string]any{"type": "stdio", "command": server["command"], "args": server["args"], "env": map[string]string{}, "enabled": true}
		}
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	if err := data.WriteFile("config.yaml", raw, 0o600); err != nil {
		return err
	}
	if raw, err = json.Marshal(map[string]any{"mcpServers": servers}); err != nil {
		return err
	}
	return data.WriteFile("mcp.json", raw, 0o600)
}

// nativeEnvironment is the adapter's own native environment, with dataDir as
// the native process sees its data directory. The adapter owns the native
// state location, including after cold resume.
func nativeEnvironment(req proto.PromptRequestPayload, dataDir string) []string {
	env := []string{"OAC_RUNTIME_MCODE_TOOL_POLICY=protected-mcp-v1"}
	if !req.DisableSubagents {
		env = append(env, "OAC_RUNTIME_MCODE_MAX_SUBAGENTS="+strconv.Itoa(*req.MaxConcurrentSubagents))
	} else {
		env = append(env, "OAC_RUNTIME_MCODE_MAX_SUBAGENTS=0")
	}
	return append(env, "MINIMAX_DATA_DIR="+dataDir, "HOME="+dataDir, "USERPROFILE="+dataDir)
}

// readData reads a file the native process wrote in its data directory,
// without leaving the Session home.
func (o launchOptions) readData(name string) ([]byte, error) {
	rel, err := filepath.Rel(o.home, filepath.Join(o.DataDir, name))
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(o.home)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.ReadFile(rel)
}
