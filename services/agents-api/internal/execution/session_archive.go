package execution

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// ArchiveManagedSession records administrative cleanup intent through the same
// execution owner that settles resources. Provider work remains in the lifecycle.
func (w *Worker) ArchiveManagedSession(ctx context.Context, tenant, session string, generation uint64) (store.ManagedSessionArchive, error) {
	return w.dispatcher.Store.ArchiveManagedSession(ctx, tenant, session, generation)
}
