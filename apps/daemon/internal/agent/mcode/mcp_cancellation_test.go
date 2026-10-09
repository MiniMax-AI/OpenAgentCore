package mcode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestEnvironmentMCPCancellationRequiresNativeLifecycle(t *testing.T) {
	for _, scenario := range []string{"unpatched-mcp", "old-mcp-lifecycle"} {
		_, err := hostExecutor(t, t.Context(), helperInstall(t, scenario, ""), workspaceRequest(t), hostSession(t, stdioBinding()))
		if err == nil || !strings.Contains(err.Error(), "native MCP lifecycle is unavailable") {
			t.Fatal("stdio MCP accepted a native owner without lifecycle support", scenario, err)
		}
	}
}

func TestEnvironmentMCPCancellationRetainsLocalFailureBeforeCancel(t *testing.T) {
	for _, outcome := range []string{"local-failure", "server-error"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			untouched := stdioBinding()
			untouched.ServerLabel = "untouched"
			host := hostSession(t, stdioBinding(), untouched, httpBinding())
			var stopped []string
			stops := 0
			host.StopMCP = func(_ context.Context, names []string) error { stops++; stopped = slices.Clone(names); return nil }
			record := filepath.Join(t.TempDir(), "calls")
			e, err := hostExecutor(t, ctx, helperInstall(t, "prepared-mcp-prior-"+outcome, record), workspaceRequest(t), host)
			if err != nil {
				t.Fatal(err)
			}
			out := make(chan proto.Envelope, 32)
			turn, err := e.StartTurn(ctx, "cancelled", proto.TextInput("wait"), out)
			if err != nil {
				t.Fatal(err)
			}
			failed := false
			for ready := false; !ready; {
				select {
				case event := <-out:
					if event.Type == proto.TypeToolCall {
						var call proto.ToolCallPayload
						if json.Unmarshal(event.Payload, &call) != nil {
							t.Fatal("invalid tool observation")
						}
						if call.ID == "native-call" && call.Stage == "after" {
							failed = call.Observation != nil && call.Observation.Status == "failed" && strings.Contains(string(call.Observation.Output), "native result")
						}
					}
					ready = event.Type == proto.TypeDelta
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			if !failed {
				t.Fatal("failure projection changed")
			}
			if err := turn.Cancel(ctx); err != nil {
				t.Fatal(err)
			}
			settlement, err := turn.AwaitSettlement(ctx)
			if err != nil || !settlement.Reusable {
				t.Fatal("cancelled owner was not reusable", settlement, err)
			}
			want := []string(nil)
			if outcome == "local-failure" {
				want = []string{"proof.server"}
			}
			if !slices.Equal(stopped, want) {
				t.Fatal("local failure and genuine MCP reply shared a settlement outcome", stopped, want)
			}
			raw, _ := os.ReadFile(record)
			if strings.Contains(string(raw), "oac/session/mcp/disconnect\n") != (len(want) > 0) {
				t.Fatalf("native disconnect did not follow the selected scope: %s", raw)
			}
			runExecutorFixtureTurn(t, e, "next", "next")
			nextOut := make(chan proto.Envelope, 8)
			next, err := e.StartTurn(ctx, "next-wait", proto.TextInput("next-wait"), nextOut)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-nextOut:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			previousStops := stops
			if err := next.Cancel(ctx); err != nil || stops != previousStops {
				t.Fatal("old unconfirmed call crossed the Turn boundary", stops, previousStops, err)
			}
		})
	}
}

func TestEnvironmentMCPCancellationFencesScopesBeforeNativeDisconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	late, untouched := stdioBinding(), stdioBinding()
	late.ServerLabel, untouched.ServerLabel = "late.server", "untouched"
	host := hostSession(t, stdioBinding(), late, untouched, httpBinding())
	started, release := make(chan []string, 1), make(chan struct{})
	host.StopMCP = func(ctx context.Context, names []string) error {
		started <- slices.Clone(names)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	record := filepath.Join(t.TempDir(), "calls")
	e, err := hostExecutor(t, ctx, helperInstall(t, "prepared-mcp-lifecycle", record), workspaceRequest(t), host)
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan proto.Envelope, 32)
	turn, err := e.StartTurn(ctx, "cancelled", proto.TextInput("wait"), out)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-out:
		if event.Type != proto.TypeToolCall {
			t.Fatal("native start was not observed", event.Type)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	done := make(chan error, 1)
	go func() { done <- turn.Cancel(ctx) }()
	select {
	case names := <-started:
		if !slices.Equal(names, []string{"late.server", "proof.server"}) {
			t.Fatal("late callback, local failure, HTTP or untouched identity was mishandled", names)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-done:
		t.Fatal("cancellation completed before ScopeClosed", err)
	default:
	}
	if _, err := os.Stat(record + ".disconnect"); !os.IsNotExist(err) {
		t.Fatal("native disconnect preceded ScopeClosed", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	settlement, err := turn.AwaitSettlement(ctx)
	if err != nil || !settlement.Reusable {
		t.Fatal("settled MCP cancellation discarded the native owner", settlement, err)
	}
	var request struct {
		SessionID string   `json:"sessionId"`
		Servers   []string `json:"servers"`
	}
	raw, err := os.ReadFile(record + ".disconnect")
	if err != nil || json.Unmarshal(raw, &request) != nil || request.SessionID != "native-1" || !slices.Equal(request.Servers, []string{"late.server", "proof.server"}) {
		t.Fatal("native disconnect changed the frozen target", string(raw), err)
	}
	for event := range out {
		if event.Type == proto.TypeError {
			t.Fatalf("cancellation emitted an error: %s", event.Payload)
		}
	}
	runExecutorFixtureTurn(t, e, "next", "next")
	if err := turn.Cancel(ctx); err != nil {
		t.Fatal("repeated cancellation lost original settlement", err)
	}
	raw, _ = os.ReadFile(record)
	if strings.Count(string(raw), "session/cancel\n") != 1 || strings.Count(string(raw), "oac/session/mcp/disconnect\n") != 1 || strings.Count(string(raw), "initialize\n") != 1 {
		t.Fatalf("old cancellation retargeted a successor or recreated the owner: %s", raw)
	}
}

func TestEnvironmentMCPCancellationRejectsUnconfirmedScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	host := hostSession(t, stdioBinding())
	host.StopMCP = func(context.Context, []string) error { return errors.New("scope close not confirmed") }
	record := filepath.Join(t.TempDir(), "calls")
	e, err := hostExecutor(t, ctx, helperInstall(t, "prepared-mcp-cancel", record), workspaceRequest(t), host)
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan proto.Envelope, 16)
	turn, err := e.StartTurn(ctx, "cancelled", proto.TextInput("wait"), out)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-out:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := turn.Cancel(ctx); err == nil {
		t.Fatal("unconfirmed scope became successful cancellation")
	}
	settlement, err := turn.AwaitSettlement(ctx)
	if err == nil || settlement.Reusable {
		t.Fatal("unconfirmed scope admitted a successor", settlement, err)
	}
	if _, err := os.Stat(record + ".disconnect"); !os.IsNotExist(err) {
		t.Fatal("unconfirmed scope reached native disconnect", err)
	}
}
