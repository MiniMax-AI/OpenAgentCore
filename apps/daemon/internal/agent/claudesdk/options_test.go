package claudesdk

import (
	"errors"
	"slices"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/modelprovider"
)

// Device credentials never stand in for the prepared provider.
func TestPreparedModelAndProviderReachTheBridge(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "device-key")
	for _, prompt := range []string{"", "instructions"} {
		start, env, err := prepareTestView(t, prepared(t, proto.PromptRequestPayload{ModelProvider: fixtureProvider(), DisableExecutionEnvironment: true, Model: "test-model", SystemPrompt: prompt}))
		if err != nil || start.Model != "test-model" || start.SystemPrompt != prompt || !slices.Contains(env, "ANTHROPIC_API_KEY=fixture-key") || slices.Contains(env, "ANTHROPIC_API_KEY=device-key") {
			t.Fatalf("model %q, system prompt %q, error %v", start.Model, start.SystemPrompt, err)
		}
	}
}

// testProxy is the gateway proxy of a test view.
const testProxy = "http://127.0.0.1:9"

// viewMCP is req's MCP as the agent host gives it to a view: the gateway
// keeps each credential and header.
func viewMCP(req agent.PrepareRequest) ([]agent.MCPBinding, error) {
	bindings, err := agent.ResolveMCPBindings(req)
	for i := range bindings {
		bindings[i].BearerToken, bindings[i].HTTPHeaders = nil, nil
	}
	return bindings, err
}

// prepareTestView prepares req as a view Executor does, in a new Session
// home, without launching the bridge.
func prepareTestView(t testing.TB, req agent.PrepareRequest) (startRequest, []string, error) {
	t.Helper()
	mcp, err := viewMCP(req)
	if err != nil {
		return startRequest{}, nil, err
	}
	home := t.TempDir()
	return prepareView(viewLayout{node: "/node", bridge: "/bridge.js"}, req, agent.ViewSession{Home: agent.ViewDir{Host: home, View: home}, Proxy: testProxy, MCP: mcp,
		Launch: func(clirunner.StartOptions) (*clirunner.Process, error) { return nil, errors.New("not launched") }})
}

// prepared is req as the registry hands it to the factory.
func prepared(t testing.TB, req proto.PromptRequestPayload) agent.PrepareRequest {
	t.Helper()
	configuration, err := Declaration.Configuration.Prepare(req)
	if err != nil {
		t.Fatal(err)
	}
	return agent.PrepareRequest{PromptRequestPayload: req, Prepared: configuration}
}

// fixtureProvider is the provider every Claude request carries.
func fixtureProvider() *modelprovider.Provider {
	return &modelprovider.Provider{Protocol: modelprovider.Anthropic, BaseURL: "https://model.example", APIKey: "fixture-key"}
}
