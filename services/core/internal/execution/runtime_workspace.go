package execution

import (
	"context"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspacefs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/workspaces"
)

func (w *Worker) Configuration(ctx context.Context) (workspacefs.Configuration, error) {
	if w.runtimes.workspaces == nil {
		return workspacefs.Configuration{}, workspaces.ErrNotConfigured
	}
	return w.runtimes.workspaces.Configuration(ctx)
}

func (w *Worker) Configure(ctx context.Context, configuration workspacefs.Configuration) (workspacefs.Configuration, error) {
	m := w.runtimes
	if m.workspaces == nil {
		return workspacefs.Configuration{}, workspaces.ErrNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	unlock, err := m.lockMutation(ctx)
	if err != nil {
		return workspacefs.Configuration{}, err
	}
	defer unlock()
	return m.workspaces.Configure(ctx, configuration, func(declaration workspacefs.Declaration) error {
		return m.deploymentService.ValidateWorkspaceConfiguration(ctx, declaration)
	})
}
