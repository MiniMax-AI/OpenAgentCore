package codex

import (
	"errors"
	"os"
	"slices"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

// mcpServersFromBindings renders a view's MCP bindings: each HTTP binding at
// its gateway URL and each stdio binding as its alias. The Harness runs the
// alias without arguments, which the native configuration reports as an empty
// list. The bindings carry no credentials; the gateway adds them.
func mcpServersFromBindings(bindings []agent.MCPBinding) map[string]mcpServerConfig {
	servers := make(map[string]mcpServerConfig, len(bindings))
	for _, binding := range bindings {
		server := mcpServerConfig{Name: binding.ServerLabel, URL: binding.ServerURL, Required: binding.Required, EnabledTools: binding.AllowedTools, ApproveTools: binding.ConnectionOrigin == "environment"}
		if binding.Stdio != nil {
			server.Command, server.Args = binding.Stdio.Server.Command, []string{}
		}
		servers[server.Name] = server
	}
	return servers
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
