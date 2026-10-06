package mcode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/managedskills"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	harnessconfiguration "github.com/MiniMax-AI/OpenAgentCore/internal/harnessconfig/mcode"
	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
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

func prepareOptions(ctx context.Context, req proto.PromptRequestPayload) (launchOptions, error) {
	return prepareOptionsWithTools(ctx, req, nil)
}

// prepareOptionsWithTools installs the managed Skills unless the workspace
// bridge's tools present the Environment's.
func prepareOptionsWithTools(ctx context.Context, req proto.PromptRequestPayload, tools *workspaceTools) (launchOptions, error) {
	var result launchOptions
	if err := validateOptions(req); err != nil {
		return result, err
	}
	if req.StrictResume && !req.DisableSubagents {
		if _, _, err := subagentReader(); err != nil {
			return result, err
		}
	}
	root, err := agent.ManagedSkillsRoot("mcode", req.AgentStateKey, req.ConversationID, req.RunID)
	if err != nil {
		return result, err
	}
	result.DataDir = filepath.Dir(root)
	result.Dir = filepath.Join(result.DataDir, "workspace")
	if err := os.MkdirAll(result.DataDir, 0o700); err != nil {
		return result, err
	}
	if err := os.MkdirAll(result.Dir, 0o700); err != nil {
		return result, err
	}
	if tools == nil {
		installed, err := managedskills.InstallManagedSkills(ctx, log.With("component", "mcode"), root, req.AgentOptions["skills"])
		if err != nil {
			return result, err
		}
		if len(installed.Warnings) > 0 {
			return result, fmt.Errorf("mcode: one or more configured Skills could not be installed")
		}
	}
	data, err := os.OpenRoot(result.DataDir)
	if err != nil {
		return result, err
	}
	defer data.Close()
	if result.Model, err = writeNativeConfig(req, data, result.DataDir, tools); err != nil {
		return result, err
	}
	opts := req.AgentOptions
	result.Env = append([]string{}, os.Environ()...)
	if req.StrictResume {
		result.Env = executionEnvironment()
	}
	if raw := opts["env"]; raw != nil && !req.StrictResume {
		env, ok := raw.(map[string]any)
		if !ok {
			return result, fmt.Errorf("mcode: env must be an object")
		}
		for key, rawValue := range env {
			value, ok := rawValue.(string)
			if !ok || key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
				return result, fmt.Errorf("mcode: invalid environment entry")
			}
			result.Env = append(result.Env, key+"="+value)
		}
	}
	result.Env = append(result.Env, nativeEnvironment(req, result.DataDir)...)
	result.MCP, err = mcpServers(opts["mcp_servers"])
	return result, err
}

func validateOptions(req proto.PromptRequestPayload) error {
	if _, err := harnessconfiguration.Configuration().PrepareHarnessConfig(req.AgentOptions); err != nil {
		return err
	}
	if req.StrictResume {
		if err := validateExecutionRequest(req); err != nil {
			return err
		}
	}
	if req.Input.HasImages() {
		return fmt.Errorf("mcode: ACP does not support attachments")
	}
	return nil
}

// writeNativeConfig writes the instructions and native configuration into the
// data directory, which the native process sees at dataDir, and returns the
// model. With tools, the workspace bridge replaces native permissions and
// sandbox, and Subagents use it too.
func writeNativeConfig(req proto.PromptRequestPayload, data *os.Root, dataDir string, tools *workspaceTools) (string, error) {
	opts := req.AgentOptions
	prompt := optionString(opts, "system_prompt")
	if override := optionString(opts, "override_system_prompt"); override != "" {
		prompt = override
	}
	if len(prompt) > 32*1024 {
		return "", fmt.Errorf("mcode: combined instructions exceed the CLI's 32 KiB limit")
	}
	if err := data.WriteFile("AGENTS.md", []byte(prompt), 0o600); err != nil {
		return "", err
	}
	config := map[string]any{"logLevel": "error", "skills": map[string]any{"external": map[string]any{"enabled": false}}}
	model := optionString(opts, "model")
	if model == "" {
		return "", fmt.Errorf("mcode: model is required")
	}
	provider, err := modelProviderConfig(opts["model_provider"], model)
	if err != nil {
		return "", err
	}
	config["custom_provider"] = map[string]any{"oac": provider}
	if req.StrictResume {
		configureTextExecution(config)
		if !req.DisableSubagents {
			config["agents"] = map[string]any{"default": map[string]any{
				"tools":        []string{"task", "task_append", "task_query", "task_output", "task_stop"},
				"builtinTools": []string{"task", "task_append", "task_query", "task_output", "task_stop"}, "skills": []string{},
				"features": map[string]bool{"mavis": false, "delegation": true, "webSearch": false},
			}}
		}
	}
	mode := optionString(opts, "mode")
	if mode == "" {
		mode = "auto"
	}
	if mode != "auto" && mode != "default" && mode != "bypassPermissions" {
		return "", fmt.Errorf("mcode: unsupported permission mode")
	}
	config["permissionMode"] = mode
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
			return "", err
		}
		if err := data.WriteFile("workspace-profile.json", raw, 0o600); err != nil {
			return "", err
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
		return "", err
	}
	if err := data.WriteFile("config.yaml", raw, 0o600); err != nil {
		return "", err
	}
	if req.StrictResume {
		if raw, err = json.Marshal(map[string]any{"mcpServers": servers}); err != nil {
			return "", err
		}
		if err := data.WriteFile("mcp.json", raw, 0o600); err != nil {
			return "", err
		}
	}
	return model, nil
}

// nativeEnvironment is the adapter's own native environment, with dataDir as
// the native process sees its data directory. The adapter owns the native
// state location, including after cold resume.
func nativeEnvironment(req proto.PromptRequestPayload, dataDir string) []string {
	var env []string
	if req.StrictResume {
		env = append(env, "OAC_RUNTIME_MCODE_TOOL_POLICY=protected-mcp-v1")
		if !req.DisableSubagents {
			env = append(env, "OAC_RUNTIME_MCODE_MAX_SUBAGENTS="+strconv.Itoa(*req.MaxConcurrentSubagents))
		} else {
			env = append(env, "OAC_RUNTIME_MCODE_MAX_SUBAGENTS=0")
		}
	}
	env = append(env, "MINIMAX_DATA_DIR="+dataDir)
	if req.StrictResume {
		env = append(env, "HOME="+dataDir, "USERPROFILE="+dataDir)
	}
	return env
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

func optionString(options map[string]any, key string) string {
	value, _ := options[key].(string)
	return value
}

func mcpServers(raw any) ([]map[string]any, error) {
	result := []map[string]any{}
	if raw == nil {
		return result, nil
	}
	servers, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mcode: mcp_servers must be an object")
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, ok := servers[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("mcode: invalid MCP server %q", name)
		}
		server := map[string]any{"name": name}
		if url := optionString(entry, "url"); url != "" {
			kind := optionString(entry, "type")
			if kind == "" {
				kind = "http"
			}
			if kind != "http" && kind != "sse" {
				return nil, fmt.Errorf("mcode: unsupported MCP transport %q", kind)
			}
			server["type"] = kind
			server["url"] = url
			headers, err := namedValues(entry["headers"])
			if err != nil {
				return nil, err
			}
			server["headers"] = headers
		} else {
			command := optionString(entry, "command")
			if command == "" {
				return nil, fmt.Errorf("mcode: MCP server %q needs command or URL", name)
			}
			server["command"] = command
			args := entry["args"]
			if args == nil {
				args = []string{}
			}
			server["args"] = args
			env, err := namedValues(entry["env"])
			if err != nil {
				return nil, err
			}
			server["env"] = env
		}
		result = append(result, server)
	}
	return result, nil
}

func namedValues(raw any) ([]map[string]string, error) {
	result := []map[string]string{}
	if raw == nil {
		return result, nil
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mcode: MCP headers/env must be an object")
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value, ok := values[key].(string)
		if !ok {
			return nil, fmt.Errorf("mcode: MCP headers/env values must be strings")
		}
		result = append(result, map[string]string{"name": key, "value": value})
	}
	return result, nil
}
