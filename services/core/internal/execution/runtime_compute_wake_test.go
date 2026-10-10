package execution

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestRuntimeWakeMapsCancellationDuringAllocationRead(t *testing.T) {
	storageError := errors.New("allocation read failed")
	for _, test := range []struct {
		name               string
		deadline           bool
		cancelBeforeResult bool
		result             error
		want               error
	}{
		{name: "cancelled query", want: ErrExecutionUnavailable},
		{name: "deadline during query", deadline: true, want: ErrExecutionUnavailable},
		{name: "storage failure", result: storageError, want: storageError},
		{name: "storage failure concurrent with cancellation", cancelBeforeResult: true, result: storageError, want: storageError},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			if test.deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), time.Millisecond)
			}
			defer cancel()
			called := false
			reader := &strictDeploymentReader{t: t, environmentAllocation: func(query context.Context, _ deployment.AllocationKey) (deployment.Allocation, error) {
				called = true
				if test.cancelBeforeResult {
					cancel()
				}
				if test.result != nil {
					return deployment.Allocation{}, test.result
				}
				if !test.deadline {
					cancel()
				}
				<-query.Done()
				return deployment.Allocation{}, fmt.Errorf("timeout: %w", query.Err())
			}}
			worker := &Worker{dispatcher: &Dispatcher{DeploymentReader: reader}, stopped: make(chan struct{})}
			if err := worker.waitRuntimeAwake(ctx, sessions.Environment{}); !errors.Is(err, test.want) || !called {
				t.Fatalf("allocation query outcome = %v, called = %v; want %v", err, called, test.want)
			}
		})
	}
}
