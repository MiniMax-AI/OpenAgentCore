//go:build unix

package claudesdk

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
)

func TestExecutorMCPStopWaitsForScopeAndRetainsTurnOwnership(t *testing.T) {
	config, req := persistentConfig(t, "mcp_cancel")
	resource, err := config.factory()(t.Context(), prepared(t, req))
	if err != nil {
		t.Fatal(err)
	}
	defer resource.Close(context.Background())
	owner := resource.(*executor)
	owner.start.Workspace = &workspaceProfile{MCP: []environmentMCPServer{
		{mcpHTTPServer: mcpHTTPServer{ServerLabel: "target"}, Command: agent.ViewAlias(0)},
		{mcpHTTPServer: mcpHTTPServer{ServerLabel: "healthy"}, Command: agent.ViewAlias(1)},
	}}
	entered, release := make(chan []string, 1), make(chan struct{})
	var calls atomic.Int32
	owner.stopMCP = func(ctx context.Context, labels []string) error {
		calls.Add(1)
		entered <- slices.Clone(labels)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	turn, out := consumeExecutorTurn(t, owner, "cancelled", "wait")
	<-out
	waitCtx, stopWait := context.WithCancel(t.Context())
	first := make(chan error, 1)
	go func() { first <- turn.Cancel(waitCtx) }()
	if labels := <-entered; !slices.Equal(labels, []string{"target"}) {
		t.Fatal(labels)
	}
	stopWait()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel waiter = %v", err)
	}
	if _, err := turn.AwaitSettlement(waitCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("scope still open: %v", err)
	}
	joined := make(chan error, 1)
	go func() { joined <- turn.Cancel(t.Context()) }()
	close(release)
	if err := <-joined; err != nil {
		t.Fatal(err)
	}
	awaitExecutorTurn(t, turn, out, true)
	next, nextOut := consumeExecutorTurn(t, owner, "successor", "hello")
	if err := turn.Cancel(t.Context()); err != nil {
		t.Fatal(err)
	}
	awaitExecutorTurn(t, next, nextOut, true)
	if calls.Load() != 1 {
		t.Fatalf("scope stops = %d", calls.Load())
	}
}

func TestExecutorMCPStopFailureAndHTTPRemainUnconfirmed(t *testing.T) {
	for _, mode := range []string{"mcp_cancel", "mcp_http"} {
		t.Run(mode, func(t *testing.T) {
			config, req := persistentConfig(t, mode)
			resource, err := config.factory()(t.Context(), prepared(t, req))
			if err != nil {
				t.Fatal(err)
			}
			defer resource.Close(context.Background())
			owner := resource.(*executor)
			owner.start.Workspace = &workspaceProfile{MCP: []environmentMCPServer{
				{mcpHTTPServer: mcpHTTPServer{ServerLabel: "target"}, Command: agent.ViewAlias(0)},
				{mcpHTTPServer: mcpHTTPServer{ServerLabel: "http", ServerURL: "http://gateway/mcp/http"}},
			}}
			var calls atomic.Int32
			owner.stopMCP = func(context.Context, []string) error {
				calls.Add(1)
				return errors.New("scope not closed")
			}
			turn, out := consumeExecutorTurn(t, owner, "cancelled", "wait")
			<-out
			if err := turn.Cancel(t.Context()); err == nil {
				t.Fatal("unconfirmed scope cancellation succeeded")
			}
			for range out {
			}
			if settled, err := turn.AwaitSettlement(t.Context()); err == nil || settled.Reusable {
				t.Fatalf("settlement = %+v, %v", settled, err)
			}
			want := int32(1)
			if mode == "mcp_http" {
				want = 0
			}
			if calls.Load() != want {
				t.Fatalf("scope stops = %d, want %d", calls.Load(), want)
			}
		})
	}
}
