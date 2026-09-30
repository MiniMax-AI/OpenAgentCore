package engine_test

import (
	"testing"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/engine/enginetest"
)

func TestMCPOriginQualification(t *testing.T) {
	for _, kind := range []string{"codex", "claude_sdk", "mcode"} {
		profile, ok := (engine.Catalog{}).Lookup(kind)
		if !ok {
			t.Fatal(kind)
		}
		for _, placement := range []string{"none", "self_hosted", "openai_hosted"} {
			for _, origin := range []string{"service", "environment", "", "unknown"} {
				servers := []proto.MCPHTTPServer{{ConnectionOrigin: origin, ServerLabel: "proof", ServerURL: "https://example.test/mcp"}}
				allowed := origin == "environment" && placement != "none" || origin == "service" && placement == "none" && kind != "mcode"
				if err := profile.ValidateMCPOrigins(&v1.Environment{Type: placement}, servers); (err == nil) != allowed {
					t.Fatalf("%s/%s/%s: %v", kind, placement, origin, err)
				}
			}
		}
	}
}
func TestMiniMaxMCPPoliciesRejectInsteadOfDropping(t *testing.T) {
	p, _ := (engine.Catalog{}).Lookup("mcode")
	empty := []string{}
	named := []string{"proof"}
	for _, allowed := range []*[]string{nil, &empty, &named} {
		for _, required := range []bool{false, true} {
			server := proto.MCPHTTPServer{ConnectionOrigin: "environment", ServerLabel: "proof", ServerURL: "https://example.test", AllowedTools: allowed, Required: required}
			err := p.ValidateTools(&v1.Environment{Type: "self_hosted"}, nil, []proto.MCPHTTPServer{server})
			if (err == nil) != (allowed == nil && !required) {
				t.Fatal("unsupported MCP policy accepted", err)
			}
		}
	}
}
func TestMCPOriginCatalogIsImmutable(t *testing.T) {
	p := enginetest.Profile(func(p *engine.Profile) {
		p.MCPOrigins = []string{"environment"}
		p.Placements = []string{"self_hosted"}
	})
	c := engine.NewCatalog(map[string]engine.Profile{"fixture": p})
	p.MCPOrigins[0] = "service"
	first, _ := c.Lookup("fixture")
	if first.MCPOrigins[0] != "environment" {
		t.Fatal("mutable source")
	}
	first.MCPOrigins[0] = "service"
	second, _ := c.Lookup("fixture")
	if second.MCPOrigins[0] != "environment" {
		t.Fatal("mutable lookup")
	}
}
