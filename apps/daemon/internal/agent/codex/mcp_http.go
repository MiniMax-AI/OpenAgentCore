package codex

import (
	"crypto/rand"
	"errors"
	"os"
	"slices"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

// One projection consumes both public and installed Runtime bindings. A non-nil
// empty public declaration still owns the complete native MCP configuration.
func runtimeMCPServers(req proto.PromptRequestPayload) (map[string]mcpServerConfig, []string, error) {
	bindings, err := agent.ResolveMCPBindings(req)
	if err != nil {
		return nil, nil, err
	}
	if bindings == nil {
		return nil, nil, nil
	}
	return mcpServersFromBindings(bindings, localworkspace.MCPStdioCommand)
}

// mcpServersFromBindings renders resolved bindings, each stdio binding with
// the command and arguments stdio gives it and each credential in a private
// environment variable.
func mcpServersFromBindings(bindings []agent.MCPBinding, stdio func(proto.EnvironmentMCP) (string, []string)) (map[string]mcpServerConfig, []string, error) {
	servers := make(map[string]mcpServerConfig, len(bindings))
	var env []string
	for _, binding := range bindings {
		if binding.ServerLabel == "codex_apps" {
			return nil, nil, errors.New("codex: reserved MCP server label")
		}
		server := mcpServerConfig{Name: binding.ServerLabel, URL: binding.ServerURL, Required: binding.Required, EnabledTools: binding.AllowedTools, ApproveTools: binding.ConnectionOrigin == "environment"}
		if binding.Stdio != nil {
			server.Command, server.Args = stdio(*binding.Stdio)
		}
		if binding.BearerToken != nil {
			server.BearerTokenEnvVar = "OAC_RUNTIME_MCP_BEARER_" + rand.Text()
			env = append(env, server.BearerTokenEnvVar+"="+*binding.BearerToken)
		}
		if len(binding.HTTPHeaders) > 0 {
			server.EnvHTTPHeaders = map[string]string{}
			for name, value := range binding.HTTPHeaders {
				reference := "OAC_RUNTIME_MCP_HEADER_" + rand.Text()
				server.EnvHTTPHeaders[name] = reference
				env = append(env, reference+"="+value)
			}
		}
		servers[server.Name] = server
	}
	return servers, env, nil
}

func configureMCP(plan *SessionPlan, servers map[string]mcpServerConfig) error {
	codexHome := plan.home.Host
	root, err := openNativeHome(codexHome)
	if err != nil {
		return errors.New("codex: public MCP requires a private native home")
	}
	// Native OAuth defaults to the global keyring. File mode confines lookup to
	// this owned history directory; never delete existing credentials to admit it.
	_, err = root.Lstat(".credentials.json")
	root.Close()
	if !errors.Is(err, os.ErrNotExist) {
		return errors.New("codex: public MCP requires a native home without stored MCP credentials")
	}
	if err := writeCodexMCPConfig(codexHome, servers); err != nil {
		return errors.New("codex: cannot write public MCP configuration")
	}
	for _, feature := range []string{"plugins", "apps"} {
		if !slices.Contains(plan.DisableFeatures, feature) {
			plan.DisableFeatures = append(plan.DisableFeatures, feature)
		}
	}
	plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"mcp_oauth_credentials_store", `"file"`})
	plan.mcpServers = servers
	return nil
}
