package claudesdk

import "github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"

// Every public Harness implements each small contract explicitly. Unsupported
// extensions return agent.ErrUnsupportedOperation without native effects.
var (
	_ agent.Executor                 = (*executor)(nil)
	_ agent.Turn                     = (*session)(nil)
	_ agent.Session                  = (*session)(nil)
	_ agent.DurableSteerer           = (*session)(nil)
	_ agent.Steerer                  = (*session)(nil)
	_ agent.FunctionResultSubmitter  = (*session)(nil)
	_ agent.PermissionResponder      = (*session)(nil)
	_ agent.UserChoiceResponder      = (*session)(nil)
	_ agent.WorkspaceReader          = (*session)(nil)
	_ agent.WorkspaceDirectoryLister = (*session)(nil)
	_ agent.WorkspaceWriter          = (*session)(nil)
	_ agent.Prepared                 = (*prepared)(nil)
	_ agent.PreparedCancellation     = (*prepared)(nil)
	_ agent.WorkspaceReader          = (*executor)(nil)
	_ agent.WorkspaceDirectoryLister = (*executor)(nil)
	_ agent.WorkspaceWriter          = (*executor)(nil)
	_ agent.WorkspaceReader          = (*prepared)(nil)
	_ agent.WorkspaceDirectoryLister = (*prepared)(nil)
	_ agent.WorkspaceWriter          = (*prepared)(nil)
)
