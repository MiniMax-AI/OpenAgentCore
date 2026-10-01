package gateway

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestMCPBrokersBothOrigins(t *testing.T) {
	sb := startSandbox(t)
	seen := make(chan string, 1)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Authorization") + " " + r.URL.RequestURI()
		io.WriteString(w, "tools")
	}))
	defer srv.Close()
	token := "vault-token"
	binding := func(origin string) proto.MCPHTTPServer {
		return proto.MCPHTTPServer{ConnectionOrigin: origin, ServerLabel: "tools", ServerURL: srv.URL + "/mcp", BearerToken: &token}
	}
	service := proto.PromptRequestPayload{DisableExecutionEnvironment: true}
	environment := proto.PromptRequestPayload{LocalEnvironment: &proto.LocalEnvironment{NetworkAccess: "enabled"}}

	for _, c := range []struct {
		origin string
		prompt proto.PromptRequestPayload
		dials  int32
	}{{"service", service, 0}, {"environment", environment, 1}} {
		before := sb.dials.Load()
		eps := serveOnLoopback(t, Config{MCP: []proto.MCPHTTPServer{binding(c.origin)}, Prompt: c.prompt, OpenNetwork: sb.open, RootCAs: trust(srv)})
		if !strings.HasPrefix(eps.MCP["tools"], "http://127.0.0.1:") || !strings.HasSuffix(eps.MCP["tools"], "/mcp") {
			t.Fatalf("%s: Harness URL %q", c.origin, eps.MCP["tools"])
		}
		req, _ := http.NewRequest("POST", eps.MCP["tools"], strings.NewReader(`{"jsonrpc":"2.0"}`))
		req.Header.Set("Authorization", "Bearer harness-value")
		resp, err := noRedirects.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != "tools" {
			t.Fatalf("%s: %d %q", c.origin, resp.StatusCode, body)
		}
		if got := <-seen; got != "Bearer vault-token /mcp" {
			t.Errorf("%s: server saw %q", c.origin, got)
		}
		if n := sb.dials.Load() - before; n != c.dials {
			t.Errorf("%s: the sandbox made %d connections, want %d", c.origin, n, c.dials)
		}
	}

	// Origin admission runs first: an environment binding needs an enabled
	// workspace network.
	if _, err := Plan(Config{MCP: []proto.MCPHTTPServer{binding("environment")}, Prompt: service, OpenNetwork: sb.open}); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("Plan with a relocated binding: %v", err)
	}
}
