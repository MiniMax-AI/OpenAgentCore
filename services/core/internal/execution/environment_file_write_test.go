package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestFileWriteBoundsReadinessAndKeepsOwnedDeadline(t *testing.T) {
	for _, shortCaller := range []bool{false, true} {
		t.Run(map[bool]string{false: "unbounded caller", true: "short caller"}[shortCaller], func(t *testing.T) {
			ctx := t.Context()
			if shortCaller {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
			}
			started := time.Now()
			worker := &Worker{stopped: make(chan struct{}), fileWrites: make(chan fileWriteRequest), dispatcher: &Dispatcher{
				DeploymentReader: &strictDeploymentReader{t: t, environmentAllocation: func(query context.Context, _ deployment.AllocationKey) (deployment.Allocation, error) {
					deadline, ok := query.Deadline()
					if !ok || deadline.After(started.Add(206*time.Second)) {
						t.Error("write readiness has no bounded budget")
					}
					return deployment.Allocation{}, deployment.ErrNotFound
				}},
				SessionsReader: initializationReadinessReader{read: func(context.Context) (sessions.Environment, error) {
					return sessions.Environment{Initialization: "complete"}, nil
				}},
			}}
			done := make(chan error, 1)
			go func() {
				_, err := worker.WriteEnvironmentFile(ctx, sessions.Environment{}, "file", []byte("ok"))
				done <- err
			}()
			var request fileWriteRequest
			select {
			case request = <-worker.fileWrites:
			case <-time.After(time.Second):
				t.Fatal("write was not admitted")
			}
			if request.deadline.Before(started.Add(204*time.Second)) || request.deadline.After(started.Add(206*time.Second)) {
				t.Fatal("worker mutation lost the existing budget", request.deadline.Sub(started))
			}
			if shortCaller {
				if err := <-done; !errors.Is(err, ErrExecutionUnavailable) {
					t.Fatal(err)
				}
				if time.Until(request.deadline) < 200*time.Second {
					t.Fatal("caller timeout shortened worker mutation ownership")
				}
			} else {
				request.result <- fileWriteResult{size: 2}
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
