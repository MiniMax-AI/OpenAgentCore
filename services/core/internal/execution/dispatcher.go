// Package execution delivers durable Turns through the existing daemon protocol.
package execution

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// Snapshot is the Session configuration frozen at creation.
type Snapshot struct {
	ModelProviderConfigured bool                         `json:"model_provider_configured,omitempty"`
	Agent                   v1.Agent                     `json:"agent"`
	Environment             *v1.Environment              `json:"environment"`
	VaultIDs                []string                     `json:"vault_ids,omitempty"`
	MCPCredentials          []store.MCPCredentialBinding `json:"mcp_credentials,omitempty"`
}

type Dispatcher struct {
	notifications *executionNotifications
	Policy
	Store    *store.Store
	Registry *runtimegateway.Registry
	// ManagedRuntimes is optional internal provisioning; it does not admit hosted API requests.
	ManagedRuntimes *RuntimeProvider
	// MaxConcurrentExecutions bounds work admitted by this Core execution owner.
	// Zero uses DefaultExecutionConcurrency. It is independent of sandbox capacity.
	MaxConcurrentExecutions int
}

type Result struct {
	EngineErrorCode  string            `json:"engine_error_code,omitempty"`
	EngineHTTPStatus *int              `json:"engine_http_status,omitempty"`
	Done             proto.DonePayload `json:"done"`
	ErrorCode        string            `json:"error_code,omitempty"`
	Error            string            `json:"error,omitempty"`
	AppliedThrough   int64             `json:"applied_through"`
}

// Run claims once before subscribing or sending. Uncertain deliveries are not replayed.
func (d *Dispatcher) Run(ctx context.Context, tenantID, sessionID, turnID string) (sessions.Turn, error) {
	session, err := d.Store.GetSession(ctx, tenantID, sessionID)
	if err != nil {
		return sessions.Turn{}, err
	}
	bound, err := d.Store.GetSessionExecutionBinding(ctx, tenantID, sessionID)
	if err != nil {
		return sessions.Turn{}, err
	}
	peer, err := d.authorizedPeer(ctx, bound.Device.ID)
	if err != nil {
		return sessions.Turn{}, err
	}
	var snapshot Snapshot
	if json.Unmarshal(session.Configuration, &snapshot) != nil || strings.TrimSpace(snapshot.Agent.Model) == "" {
		return sessions.Turn{}, store.ErrInvalidInput
	}
	caps, err := d.engineCapabilities(peer, session.Engine, snapshot)
	if err != nil {
		return sessions.Turn{}, err
	}
	if !environmentNone(snapshot) {
		return sessions.Turn{}, store.ErrInvalidInput
	}
	text, through, err := d.initialInput(ctx, tenantID, sessionID, turnID)
	if err != nil {
		return sessions.Turn{}, err
	}
	if err := d.messageInputSupport(peer, session.Engine, snapshot, text); err != nil {
		return sessions.Turn{}, err
	}
	req, err := d.executionRequest(ctx, session, snapshot, caps, bound)
	if err != nil {
		return sessions.Turn{}, err
	}
	req.DisableExecutionEnvironment = true
	prepared, err := d.prepareTurnExecutor(ctx, peer, tenantID, sessionID, turnID, req, sessions.TurnQueued)
	if err != nil {
		return sessions.Turn{}, err
	}
	defer prepared.close()
	if _, err := d.Store.TransitionTurn(ctx, tenantID, sessionID, turnID, store.TurnTransition{ExpectedStatus: sessions.TurnQueued, Status: sessions.TurnInProgress}); err != nil {
		return sessions.Turn{}, err
	}
	req.ConversationID, req.RunID, req.Input = sessionID, turnID, text
	release, err := peer.TrackExecutionDelivery(req.RunID)
	if err != nil {
		return d.finishRun(tenantID, sessionID, turnID, snapshot.Agent.Model, Result{ErrorCode: "delivery_unknown", AppliedThrough: through}, sessions.TurnFailed)
	}
	defer release()
	result, status := d.deliver(ctx, tenantID, sessionID, peer, req, through, prepared)
	return d.finishRun(tenantID, sessionID, turnID, snapshot.Agent.Model, result, status)
}

func (d *Dispatcher) finishRun(tenantID, sessionID, turnID, model string, result Result, status string) (sessions.Turn, error) {
	if result.Done.Usage.Model == "" {
		result.Done.Usage.Model = model
	}
	nativeID, _ := result.Done.Metadata[proto.DoneMetaAgentSessionID].(string)
	finishCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 512*1024 || len(nativeID) > 512 {
		result = Result{ErrorCode: "invalid_executor_result", AppliedThrough: result.AppliedThrough}
		encoded, _ = json.Marshal(result)
		status, nativeID = sessions.TurnFailed, ""
	}
	turn, err := d.Store.CompleteExecution(finishCtx, tenantID, sessionID, turnID, status, encoded, nativeID, result.AppliedThrough)
	if errors.Is(err, store.ErrUnappliedInputs) {
		result.ErrorCode = "input_not_applied"
		encoded, _ = json.Marshal(result)
		turn, err = d.Store.CompleteExecution(finishCtx, tenantID, sessionID, turnID, sessions.TurnFailed, encoded, nativeID, result.AppliedThrough)
	}
	if err == nil {
		d.observeDeploymentProvider(tenantID, sessionID, turn)
	}
	return turn, err
}
