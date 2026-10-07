package codex

import "github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"

// Every public Harness implements each small contract explicitly. Unsupported
// extensions return agent.ErrUnsupportedOperation without native effects.
var (
	_ agent.Executor                 = (*Executor)(nil)
	_ agent.Turn                     = (*Session)(nil)
	_ agent.Session                  = (*Session)(nil)
	_ agent.DurableSteerer           = (*Session)(nil)
	_ agent.Steerer                  = (*Session)(nil)
	_ agent.FunctionResultSubmitter  = (*Session)(nil)
	_ agent.WorkspaceDirectoryLister = (*Session)(nil)
	_ agent.WorkspaceWriter          = (*Session)(nil)
	_ agent.WorkspaceDirectoryLister = (*Executor)(nil)
	_ agent.WorkspaceWriter          = (*Executor)(nil)
)
