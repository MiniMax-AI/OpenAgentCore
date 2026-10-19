package codex

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// One projection consumes both public and installed Runtime bindings.
func runtimeMCPServers(req proto.PromptRequestPayload) (map[string]mcpServerConfig, []string, error) {
	bindings, err := agent.ResolveMCPBindings(req)
	if err != nil {
		return nil, nil, err
	}
	if bindings == nil {
		return nil, nil, nil
	}
	servers := make(map[string]mcpServerConfig, len(bindings))
	var env []string
	for _, binding := range bindings {
		if binding.ServerLabel == "codex_apps" {
			return nil, nil, errors.New("codex: reserved MCP server label")
		}
		server := mcpServerConfig{Name: binding.ServerLabel, URL: binding.ServerURL, Required: binding.Required, EnabledTools: binding.AllowedTools, HTTPHeaders: binding.HTTPHeaders}
		if binding.Stdio != nil {
			server.Command, server.Args = localworkspace.MCPStdioCommand(*binding.Stdio)
		}
		if process := binding.Process; process != nil {
			server.Command, server.Args, server.Cwd, server.EnvVars = process.Command, process.Args, process.Cwd, process.EnvVars
		}
		if binding.BearerToken != nil {
			server.BearerTokenEnvVar = "OAC_RUNTIME_MCP_BEARER_" + rand.Text()
			env = append(env, server.BearerTokenEnvVar+"="+*binding.BearerToken)
		}
		servers[server.Name] = server
	}
	return servers, env, nil
}

func configureMCP(plan *SessionPlan, servers map[string]mcpServerConfig) error {
	codexHome := nativeHomeFromPlan(*plan)
	if !filepath.IsAbs(codexHome) {
		return errors.New("codex: public MCP requires a private native home")
	}
	// Native OAuth defaults to the global keyring. File mode confines lookup to
	// this owned history directory; never delete existing credentials to admit it.
	if _, err := os.Lstat(filepath.Join(codexHome, ".credentials.json")); !errors.Is(err, os.ErrNotExist) {
		return errors.New("codex: public MCP requires a native home without stored MCP credentials")
	}
	if err := writeCodexMCPConfig(codexHome, servers); err != nil {
		return errors.New("codex: cannot write public MCP configuration")
	}
	// Native shell tools must not inherit the per-server secret variables.
	excludeFromShellEnvironment(plan, "OAC_RUNTIME_MCP_*")
	plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"mcp_oauth_credentials_store", `"file"`})
	plan.mcpServers = servers
	return nil
}
