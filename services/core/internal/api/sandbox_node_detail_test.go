package api

import (
	"context"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
)

func TestSandboxNodeDetailValidationAndAuthentication(t *testing.T) {
	deps, fakes := sandboxFakes(t)
	// The deployment rejects an unknown range or a malformed node ID.
	fakes.deployment.nodeDetail = func(context.Context, string, string) (deployment.NodeDetail, error) {
		return deployment.NodeDetail{}, deployment.ErrInvalidInput
	}
	h := newTestHandler(t, deps)
	path := "/core/v1/sandbox/nodes/11111111-1111-4111-8111-111111111111"
	for _, token := range []string{"", "caller", "enrollment", "node"} {
		if w := projectKeyHTTP(h, "GET", path, token, ""); w.Code != 401 {
			t.Fatal(token, w.Code)
		}
	}
	for _, query := range []string{"?range=", "?range=7d", "?range=1h&range=6h", "?tenant_id=x", "?range=1h;bad=1", "?range=%xx"} {
		if w := projectKeyHTTP(h, "GET", path+query, "administrator", ""); w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	if w := projectKeyHTTP(h, "GET", "/core/v1/sandbox/nodes/invalid", "administrator", ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
}

func TestSandboxNodeResponsesCarryRecommendedCapacity(t *testing.T) {
	deps, fakes := sandboxFakes(t)
	node := deployment.Node{RecommendedCapacity: &deployment.Capacity{MaxActive: 3, MaxRetained: 14}}
	fakes.deployment.listNodes = func(context.Context) ([]deployment.Node, error) { return []deployment.Node{node}, nil }
	fakes.deployment.nodeDetail = func(context.Context, string, string) (deployment.NodeDetail, error) {
		return deployment.NodeDetail{Node: node}, nil
	}
	h := newTestHandler(t, deps)
	for _, path := range []string{"/core/v1/sandbox/nodes", "/core/v1/sandbox/nodes/11111111-1111-4111-8111-111111111111"} {
		w := projectKeyHTTP(h, "GET", path, "administrator", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"recommended_capacity":{"max_active":3,"max_retained":14}`) {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
}
