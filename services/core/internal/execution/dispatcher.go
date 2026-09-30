// Package execution delivers durable Turns through the existing daemon protocol.
package execution

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// Snapshot is resolved internally; Daemon is not a public environment wire type.
type Snapshot struct {
	ModelProviderConfigured bool                         `json:"model_provider_configured,omitempty"`
	Agent                   v1.Agent                     `json:"agent"`
	Daemon                  *DaemonConfig                `json:"daemon"`
	Environment             *v1.Environment              `json:"environment"`
	VaultIDs                []string                     `json:"vault_ids,omitempty"`
	MCPCredentials          []store.MCPCredentialBinding `json:"mcp_credentials,omitempty"`
}

type DaemonConfig struct {
	WorkDir string `json:"work_dir"`
}

type Dispatcher struct {
	notifications *executionNotifications
	Policy
	Store    *store.Store
	Registry *gateway.Registry
	// Options optionally supplies native adapter options for Sessions that need
	// no frozen model provider (environment none and legacy daemon Sessions).
	// The server command leaves it nil since the operator options file was
	// retired; native tests use it to reach synthetic model servers. It is never
	// consulted for openai_hosted or self_hosted Sessions.
	Options func(context.Context, store.Session) (map[string]any, error)
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
func (d *Dispatcher) Run(ctx context.Context, tenantID, sessionID, turnID string) (store.Turn, error) {
	session, err := d.Store.GetSession(ctx, tenantID, sessionID)
	if err != nil {
		return store.Turn{}, err
	}
	bound, err := d.Store.GetSessionExecutionBinding(ctx, tenantID, sessionID)
	if err != nil {
		return store.Turn{}, err
	}
	peer, err := d.authorizedPeer(ctx, bound.Device.ID)
	if err != nil {
		return store.Turn{}, err
	}
	var snapshot Snapshot
	if json.Unmarshal(session.Configuration, &snapshot) != nil || strings.TrimSpace(snapshot.Agent.Model) == "" {
		return store.Turn{}, store.ErrInvalidInput
	}
	caps, err := d.engineCapabilities(peer, session.Engine, snapshot)
	if err != nil {
		return store.Turn{}, err
	}
	workDir, noEnvironment, err := resolveExecutionEnvironment(snapshot)
	if err != nil {
		return store.Turn{}, err
	}
	text, through, err := d.initialInput(ctx, tenantID, sessionID, turnID)
	if err != nil {
		return store.Turn{}, err
	}
	if err := d.messageInputSupport(peer, session.Engine, snapshot, text); err != nil {
		return store.Turn{}, err
	}
	req, err := d.executionRequest(ctx, session, snapshot, caps, bound)
	if err != nil {
		return store.Turn{}, err
	}
	req.WorkDir, req.DisableExecutionEnvironment = workDir, noEnvironment
	prepared, err := d.prepareTurnExecutor(ctx, peer, tenantID, sessionID, turnID, req, store.TurnQueued)
	if err != nil {
		return store.Turn{}, err
	}
	defer prepared.close()
	if _, err := d.Store.TransitionTurn(ctx, tenantID, sessionID, turnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress}); err != nil {
		return store.Turn{}, err
	}
	req.ConversationID, req.RunID, req.Input = sessionID, turnID, text
	release, err := peer.TrackExecutionDelivery(req.RunID)
	if err != nil {
		return d.finishRun(tenantID, sessionID, turnID, snapshot.Agent.Model, Result{ErrorCode: "delivery_unknown", AppliedThrough: through}, store.TurnFailed)
	}
	defer release()
	result, status := d.deliver(ctx, tenantID, sessionID, peer, req, through, prepared)
	return d.finishRun(tenantID, sessionID, turnID, snapshot.Agent.Model, result, status)
}

func (d *Dispatcher) finishRun(tenantID, sessionID, turnID, model string, result Result, status string) (store.Turn, error) {
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
		status, nativeID = store.TurnFailed, ""
	}
	turn, err := d.Store.CompleteExecution(finishCtx, tenantID, sessionID, turnID, status, encoded, nativeID, result.AppliedThrough)
	if errors.Is(err, store.ErrUnappliedInputs) {
		result.ErrorCode = "input_not_applied"
		encoded, _ = json.Marshal(result)
		turn, err = d.Store.CompleteExecution(finishCtx, tenantID, sessionID, turnID, store.TurnFailed, encoded, nativeID, result.AppliedThrough)
	}
	if err == nil {
		d.observeDeploymentProvider(tenantID, sessionID, turn)
	}
	return turn, err
}
