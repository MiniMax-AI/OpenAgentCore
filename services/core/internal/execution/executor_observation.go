package execution

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
)

// These durations use one Core process monotonic clock. They describe control
// readiness and input-to-first-text latency, not model-only or native tool time.
func recordExecutorReadiness(p *preparedStart, status proto.PreparationStatusPayload) {
	obslog.Bg().Info("executor ready", "request_id", p.requestID,
		"executor_id", status.ExecutorID, "reused", status.Reused,
		"control_ready_ms", time.Since(p.createdAt).Milliseconds())
}

// The started receipt confirms adapter ownership, not model consumption.
func recordExecutorStart(p *preparedStart, turn string) {
	obslog.Bg().Info("executor start acknowledged", "turn_id", turn,
		"request_id", p.requestID, "executor_id", p.executorID,
		"start_control_ms", time.Since(p.startSentAt).Milliseconds())
}

func recordFirstText(ctx context.Context, session, turn string, submitted time.Time) {
	obslog.Bg().InfoContext(ctx, "executor first text", "session_id", session,
		"turn_id", turn, "input_to_first_text_ms", time.Since(submitted).Milliseconds())
}

func hasText(env proto.Envelope) bool {
	switch env.Type {
	case proto.TypeDelta:
		var text proto.DeltaPayload
		return env.DecodePayload(&text) == nil && text.Delta != ""
	case proto.TypeOutputMessage:
		var text proto.OutputMessagePayload
		return env.DecodePayload(&text) == nil && text.Text != nil && *text.Text != ""
	}
	return false
}
