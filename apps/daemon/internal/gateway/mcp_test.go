package gateway

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestMCPBrokersBothOrigins(t *testing.T) {
	sb := startSandbox(t)
	seen := make(chan string, 1)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Authorization") + " " + strings.Join(r.Header.Values("X-Tenant"), ",") + " " + r.URL.RequestURI()
		w.Header().Set("X-Echo", "tenant-secret")
		w.Header().Set("X-Plain", "visible")
		io.WriteString(w, "tools")
	}))
	defer srv.Close()
	token := "vault-token"
	binding := func(origin string) agent.MCPBinding {
		return agent.MCPBinding{ConnectionOrigin: origin, ServerLabel: "tools", Transport: "http", ServerURL: srv.URL + "/mcp",
			BearerToken: &token, HTTPHeaders: map[string]string{"x-tenant": " tenant-secret "}}
	}
	service := proto.PromptRequestPayload{DisableExecutionEnvironment: true}
	environment := proto.PromptRequestPayload{LocalEnvironment: &proto.LocalEnvironment{}}

	for _, c := range []struct {
		origin string
		prompt proto.PromptRequestPayload
		dials  int32
	}{{"service", service, 0}, {"environment", environment, 1}} {
		before := sb.dials.Load()
		eps := serveOnLoopback(t, Config{Model: model, MCP: []agent.MCPBinding{binding(c.origin)}, Prompt: c.prompt, OpenNetwork: sb.open, RootCAs: trust(srv)})
		if !strings.HasPrefix(eps.MCP["tools"], "http://127.0.0.1:") || !strings.HasSuffix(eps.MCP["tools"], "/mcp") {
			t.Fatalf("%s: Harness URL %q", c.origin, eps.MCP["tools"])
		}
		req, _ := http.NewRequest("POST", eps.MCP["tools"], strings.NewReader(`{"jsonrpc":"2.0"}`))
		req.Header.Set("Authorization", "Bearer harness-value")
		req.Header.Set("X-TENANT", "harness-value")
		resp, err := noRedirects.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != "tools" {
			t.Fatalf("%s: %d %q", c.origin, resp.StatusCode, body)
		}
		if got := <-seen; got != "Bearer vault-token tenant-secret /mcp" {
			t.Errorf("%s: server saw %q", c.origin, got)
		}
		if resp.Header.Get("X-Echo") != "" || resp.Header.Get("X-Plain") != "visible" {
			t.Errorf("%s: an injected header value reached the Harness, or a plain one did not", c.origin)
		}
		if n := sb.dials.Load() - before; n != c.dials {
			t.Errorf("%s: the sandbox made %d connections, want %d", c.origin, n, c.dials)
		}
	}

	// Origin admission runs first: an environment binding needs an enabled
	// workspace network.
	if _, err := Plan(Config{Model: model, MCP: []agent.MCPBinding{binding("environment")}, Prompt: service, OpenNetwork: sb.open}); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("Plan with a relocated binding: %v", err)
	}
	// The gateway relays HTTP only, and the bearer token owns Authorization.
	stdio := binding("service")
	stdio.Transport, stdio.ServerURL, stdio.BearerToken, stdio.HTTPHeaders = "stdio", "", nil, nil
	twice := binding("service")
	twice.HTTPHeaders = map[string]string{"Authorization": "Basic other"}
	// No credential crosses a network in plaintext, and userinfo is none.
	plain := binding("environment")
	plain.ServerURL, plain.BearerToken, plain.HTTPHeaders = "http://mcp.test/mcp", nil, map[string]string{"X-Api-Key": "header-secret"}
	query := binding("environment")
	query.ServerURL, query.BearerToken, query.HTTPHeaders = "http://mcp.test/mcp?api_key=query-secret", nil, nil
	userinfo := binding("service")
	userinfo.ServerURL = "https://user:info-secret@mcp.test/mcp"
	for name, c := range map[string]struct {
		b      agent.MCPBinding
		prompt proto.PromptRequestPayload
	}{"stdio": {stdio, service}, "Authorization twice": {twice, service}, "headers over http": {plain, environment}, "a query over http": {query, environment}, "userinfo": {userinfo, service}} {
		_, err := Plan(Config{Model: model, MCP: []agent.MCPBinding{c.b}, Prompt: c.prompt, OpenNetwork: sb.open})
		if !errors.Is(err, ErrInvalidConfig) || strings.Contains(err.Error(), "secret") {
			t.Errorf("Plan with %s: %v", name, err)
		}
	}
}

// The Harness's URL carries no query; the listener relays to exactly the
// server URL, refuses any other target and keeps the query out of response
// headers.
func TestMCPServesOnlyItsServerURL(t *testing.T) {
	seen := make(chan string, 8)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.URL.RequestURI():
		default:
		}
		w.Header().Set("Location", r.URL.RequestURI())
		w.Header().Set("X-Key", r.URL.Query().Get("key"))
		w.Header().Set("X-Plain", "visible")
	}))
	defer srv.Close()
	const query = "tenant=acme&key=query%2Bsecret"
	b := agent.MCPBinding{ConnectionOrigin: "service", ServerLabel: "tools", Transport: "http", ServerURL: srv.URL + "/mcp?" + query}
	eps := serveOnLoopback(t, Config{Model: model, MCP: []agent.MCPBinding{b}, Prompt: proto.PromptRequestPayload{DisableExecutionEnvironment: true}, RootCAs: trust(srv)})
	harness := eps.MCP["tools"]
	if !strings.HasPrefix(harness, "http://127.0.0.1:") || !strings.HasSuffix(harness, "/mcp") || strings.Contains(harness, "?") {
		t.Fatalf("Harness URL %q", harness)
	}
	base := strings.TrimSuffix(harness, "/mcp")
	for _, c := range []struct {
		url    string
		status int
	}{{harness, 200}, {harness + "?tenant=b", 400}, {harness + "?", 400}, {base + "/other", 404}, {base + "/mcp/", 404}} {
		resp, err := noRedirects.Get(c.url)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.status {
			t.Errorf("GET %s: %d, want %d", strings.TrimPrefix(c.url, base), resp.StatusCode, c.status)
		}
		if c.status == 200 && (resp.Header.Get("Location") != "" || resp.Header.Get("X-Key") != "" || resp.Header.Get("X-Plain") != "visible") {
			t.Errorf("response headers %v", resp.Header)
		}
	}
	if got := <-seen; got != "/mcp?"+query || len(seen) != 0 {
		t.Errorf("the server saw %q and %d more requests", got, len(seen))
	}
}
