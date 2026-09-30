package execution

import (
	"context"
	"encoding/json"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// observeDeploymentProvider runs strictly after the terminal commit. Its pool
// and lock timeouts cannot cancel the execution lease or change the already
// committed public outcome.
func (d *Dispatcher) observeDeploymentProvider(tenantID, sessionID string, turn sessions.Turn) {
	var result Result
	if turn.Status == sessions.TurnFailed && json.Unmarshal(turn.Outcome, &result) != nil {
		return
	}
	if !modelconfiguration.ShouldObserveProvider(turn.Status, result.ErrorCode, result.EngineErrorCode) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := d.Observer.ObserveDeploymentModelProvider(ctx, modelconfiguration.Observation{TenantID: tenantID, SessionID: sessionID, TurnID: turn.ID}); err != nil {
		log.Warn(ctx, "Deployment model provider observation unavailable")
	}
}
