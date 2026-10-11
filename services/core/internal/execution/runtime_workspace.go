package execution

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspaces"
)

func (w *Worker) Selection(ctx context.Context) (workspaces.Selection, error) {
	if w.runtimes.workspaces == nil {
		return workspaces.Selection{}, workspaces.ErrNotConfigured
	}
	return w.runtimes.workspaces.Selection(ctx)
}

func (w *Worker) Configure(ctx context.Context, input workspaces.SelectionInput) (workspaces.Selection, error) {
	m := w.runtimes
	if m.workspaces == nil {
		return workspaces.Selection{}, workspaces.ErrNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	unlock, err := m.lockMutation(ctx)
	if err != nil {
		return workspaces.Selection{}, err
	}
	defer unlock()
	return m.workspaces.Configure(ctx, input, func(declaration workspacefs.Declaration) error {
		return m.deploymentService.ValidateWorkspaceConfiguration(ctx, declaration)
	})
}
