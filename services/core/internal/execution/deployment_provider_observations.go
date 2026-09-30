package execution

import (
	"context"
	"encoding/json"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

// Observation is strictly after terminal commit. Its pool/lock timeout cannot
// cancel the execution lease or change the already committed public outcome.
func (d *Dispatcher) observeDeploymentProvider(tenantID, sessionID string, turn store.Turn) {
	if turn.Status != store.TurnCompleted {
		if turn.Status != store.TurnFailed {
			return
		}
		var result Result
		if json.Unmarshal(turn.Outcome, &result) != nil || result.ErrorCode != "engine_failed" {
			return
		}
		code, _ := proto.NormalizeEngineFailure(result.EngineErrorCode, nil)
		if code == "" || code == "context_length_exceeded" || code == "cyber_policy" {
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := d.Store.ObserveDeploymentModelProvider(ctx, tenantID, sessionID, turn.ID); err != nil {
		log.Warn(ctx, "Deployment model provider observation unavailable")
	}
}
