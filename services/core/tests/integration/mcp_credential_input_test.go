package integration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
)

// A message that would start a Turn is rejected once the Credential its
// Session bound is deleted, whether the tool named it or creation selected
// it, but a key the Session already used still replays or conflicts.
func TestWorkerRejectsInputWhoseCredentialWasDeleted(t *testing.T) {
	for _, mode := range []string{"auto-selected", "explicit"} {
		t.Run(mode, func(t *testing.T) {
			h := newFunctionHarnessWith(t, functionConfiguration)
			configuration, _ := mcpBearerWorkerConfiguration(t, h)
			var snapshot execution.Snapshot
			if err := json.Unmarshal([]byte(configuration), &snapshot); err != nil {
				t.Fatal(err)
			}
			binding := snapshot.MCPCredentials[0]
			if mode == "explicit" {
				configuration = strings.Replace(configuration, `"credential_id":null`, `"credential_id":"`+binding.CredentialID+`"`, 1)
			}
			var err error
			h.session, err = h.s.CreateSession(t.Context(), h.tenant, sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "bound", Configuration: json.RawMessage(configuration)})
			if err != nil {
				t.Fatal(err)
			}
			h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{
				Streaming: proto.CapabilitySupported, DurableTurns: proto.CapabilitySupported, DurableInputReceipts: proto.CapabilitySupported, EnvironmentNone: proto.CapabilitySupported,
				ExecutionControls: proto.CapabilitySupported, SubagentControl: proto.CapabilitySupported, ToolObservations: proto.CapabilitySupported, Preparation: proto.CapabilitySupported,
				MCPHTTPTools: proto.CapabilitySupported, MCPHTTPBearerAuth: proto.CapabilitySupported, MCPHTTPRequired: proto.CapabilitySupported})}}})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			worker := startWorker(t, ctx, h.s, h.d)
			done := make(chan error, 1)
			go func() { done <- worker.Run(ctx) }()
			defer func() {
				cancel()
				<-done
			}()
			first, err := worker.SubmitInputs(ctx, h.tenant, h.session.ID, "first", []sessions.Input{messageInput("one")})
			if err != nil {
				t.Fatal(err)
			}
			transition(t, h.s, h.tenant, h.session.ID, first[0].TurnID, sessions.TurnQueued, sessions.TurnCancelled)
			_, vaultService, err := fixtureVaults(h.s)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := vaultService.DeleteCredential(ctx, vaults.DeleteCredential{TenantID: h.tenant, VaultID: binding.VaultID, CredentialID: binding.CredentialID}); err != nil {
				t.Fatal(err)
			}
			var turns int
			_, err = worker.SubmitInputs(ctx, h.tenant, h.session.ID, "second", []sessions.Input{messageInput("two")})
			var selection *vaults.MCPCredentialSelectionError
			if !errors.As(err, &selection) || selection.Conflict {
				t.Fatal("input after deletion was admitted", err)
			}
			if err := h.s.pool.QueryRow(ctx, "SELECT count(*) FROM turns WHERE session_id=$1", h.session.ID).Scan(&turns); err != nil || turns != 1 {
				t.Fatal("rejected input created a Turn", turns, err)
			}
			replay, err := worker.SubmitInputs(ctx, h.tenant, h.session.ID, "first", []sessions.Input{messageInput("one")})
			if err != nil || len(replay) != 1 || replay[0].TurnID != first[0].TurnID {
				t.Fatal("retry of an admitted batch changed", replay, err)
			}
			if _, err := worker.SubmitInputs(ctx, h.tenant, h.session.ID, "first", []sessions.Input{messageInput("other")}); !errors.Is(err, sessions.ErrIdempotencyConflict) {
				t.Fatal("reused key with another batch", err)
			}
		})
	}
}
