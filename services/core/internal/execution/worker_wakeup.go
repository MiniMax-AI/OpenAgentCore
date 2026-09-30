package execution

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// wakeScheduler only hints at committed work. The existing loop retains lease,
// capacity and Session ownership; polling recovers absent or coalesced hints.
func (w *Worker) wakeScheduler() {
	select {
	case w.scheduleWake <- struct{}{}:
	default:
	}
}

func (w *Worker) admitInputs(ctx context.Context, tenant, session, key string, inputs []sessions.Input) ([]sessions.InputReceipt, error) {
	receipts, err := w.admission.SubmitInputs(ctx, tenant, session, key, inputs)
	if err == nil {
		w.wakeScheduler()
		w.dispatcher.notifications.notify(tenant, session)
	}
	return receipts, err
}
