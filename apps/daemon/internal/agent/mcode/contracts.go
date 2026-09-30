package mcode

import "github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"

// Every public Harness implements each small contract explicitly. Unsupported
// extensions return agent.ErrUnsupportedOperation without native effects.
var (
	_ agent.Executor                 = (*executor)(nil)
	_ agent.Turn                     = (*Session)(nil)
	_ agent.Session                  = (*Session)(nil)
	_ agent.DurableSteerer           = (*Session)(nil)
	_ agent.Steerer                  = (*Session)(nil)
	_ agent.FunctionResultSubmitter  = (*Session)(nil)
	_ agent.PermissionResponder      = (*Session)(nil)
	_ agent.UserChoiceResponder      = (*Session)(nil)
	_ agent.WorkspaceReader          = (*Session)(nil)
	_ agent.WorkspaceDirectoryLister = (*Session)(nil)
	_ agent.WorkspaceWriter          = (*Session)(nil)
	_ agent.Prepared                 = (*prepared)(nil)
	_ agent.PreparedCancellation     = (*prepared)(nil)
	_ agent.WorkspaceReader          = (*executor)(nil)
	_ agent.WorkspaceDirectoryLister = (*executor)(nil)
	_ agent.WorkspaceWriter          = (*executor)(nil)
	_ agent.WorkspaceReader          = (*prepared)(nil)
	_ agent.WorkspaceDirectoryLister = (*prepared)(nil)
	_ agent.WorkspaceWriter          = (*prepared)(nil)
)
