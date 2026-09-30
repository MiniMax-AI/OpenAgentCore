package execution

import (
	"context"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// ArchiveManagedSession records administrative cleanup intent through the same
// execution owner that settles resources. Provider work remains in the lifecycle.
func (w *Worker) ArchiveManagedSession(ctx context.Context, tenant, session string, generation uint64) (sessions.ManagedArchive, error) {
	return w.dispatcher.Store.ArchiveManagedSession(ctx, tenant, session, generation)
}
