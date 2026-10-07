package mcode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig"
	harnessconfiguration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/mcode"
)

type launchOptions struct {
	Dir, DataDir, Model string
	Env                 []string
	MCP                 []map[string]any
	// bindings are the effective MCP bindings rendered into MCP; native MCP
	// tool calls are observed against them.
	bindings []agent.MCPBinding
	// start runs the native process; nil selects clirunner.Start. script is
	// the CLI entry when the binary is node rather than the CLI itself.
	start  func(clirunner.StartOptions) (*clirunner.Process, error)
	script string
	// spawn runs the Subagent history reader beside the native process in an
	// agent-host view, with reader's node, script and data directory as the
	// view presents them. nil runs the installed reader on the host over
	// DataDir.
	spawn  func(clirunner.StartOptions) (*clirunner.Process, error)
	reader clirunner.StartOptions
	// home is an agent-host view's Session home on the host. It contains
	// DataDir and belongs to the Session user, so the daemon reads DataDir
	// only within it.
	home string
}

func prepareOptions(req proto.PromptRequestPayload) (launchOptions, error) {
	return prepareOptionsWithTools(req, nil)
}

// prepareOptionsWithTools prepares the Session's native data directory. With
// tools, the workspace bridge presents the Environment's workspace and Skills.
func prepareOptionsWithTools(req proto.PromptRequestPayload, tools *workspaceTools) (launchOptions, error) {
	var result launchOptions
	prepared, err := validateOptions(req)
	if err != nil {
		return result, err
	}
	if !req.DisableSubagents {
		if _, _, err := subagentReader(); err != nil {
			return result, err
		}
	}
	if result.DataDir, err = dataDirectory(req); err != nil {
		return result, err
	}
	result.Dir = filepath.Join(result.DataDir, "workspace")
	if err := os.MkdirAll(result.DataDir, 0o700); err != nil {
		return result, err
	}
	if err := os.MkdirAll(result.Dir, 0o700); err != nil {
		return result, err
	}
	data, err := os.OpenRoot(result.DataDir)
	if err != nil {
		return result, err
	}
	defer data.Close()
	if err := writeNativeConfig(req, prepared, data, result.DataDir, tools); err != nil {
		return result, err
	}
	result.Model = prepared.Model
	result.Env = append(executionEnvironment(), nativeEnvironment(req, result.DataDir)...)
	result.MCP = []map[string]any{}
	return result, nil
}

// validateOptions checks the request before any native effect and returns its
// model configuration.
func validateOptions(req proto.PromptRequestPayload) (harnessconfig.PreparedConfiguration, error) {
	prepared, err := harnessconfiguration.Configuration().Prepare(req)
	if err != nil {
		return prepared, err
	}
	if err := validateExecutionRequest(req); err != nil {
		return prepared, err
	}
	if req.Input.HasImages() {
		return prepared, fmt.Errorf("mcode: ACP does not support attachments")
	}
	return prepared, nil
}

// writeNativeConfig writes the instructions and native configuration into the
// data directory, which the native process sees at dataDir. With tools, the
// workspace bridge replaces native permissions and sandbox, and Subagents use
// it too.
func writeNativeConfig(req proto.PromptRequestPayload, prepared harnessconfig.PreparedConfiguration, data *os.Root, dataDir string, tools *workspaceTools) error {
	if len(req.SystemPrompt) > 32*1024 {
		return fmt.Errorf("mcode: combined instructions exceed the CLI's 32 KiB limit")
	}
	if err := data.WriteFile("AGENTS.md", []byte(req.SystemPrompt), 0o600); err != nil {
		return err
	}
	config := map[string]any{"logLevel": "error", "skills": map[string]any{"external": map[string]any{"enabled": false}}}
	config["custom_provider"] = map[string]any{"oac": modelProviderConfig(prepared.Provider, prepared.Model)}
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
			selected := config["agents"].(map[string]any)["default"].(map[string]any)
			selected["skills"] = tools.skills
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
// without leaving the Session home in a view.
func (o launchOptions) readData(name string) ([]byte, error) {
	trusted := o.home
	if trusted == "" {
		trusted = o.DataDir
	}
	rel, err := filepath.Rel(trusted, filepath.Join(o.DataDir, name))
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(trusted)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.ReadFile(rel)
}

// dataDirectory returns the native data directory of the request's agent
// state. It never derives runtime state from the subprocess cwd.
func dataDirectory(req proto.PromptRequestPayload) (string, error) {
	root, err := paths.Root()
	if err != nil {
		return "", fmt.Errorf("mcode: resolve data directory: %w", err)
	}
	parts := []string{root, "runtime", "mcode", "state"}
	for _, part := range strings.Split(req.AgentStateKey, "/") {
		if safe := safePathPart(part); safe != "" {
			parts = append(parts, safe)
		}
	}
	if len(parts) == 4 {
		return "", fmt.Errorf("mcode: invalid agent state key %q", req.AgentStateKey)
	}
	return filepath.Join(parts...), nil
}

func safePathPart(value string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	value = b.String()
	if value == "." || value == ".." {
		return ""
	}
	return value
}
