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

type initializationReadinessReader struct {
	sessions.Reader
	read func(context.Context) (sessions.Environment, error)
}

func (r initializationReadinessReader) GetEnvironment(ctx context.Context, _, _ string) (sessions.Environment, error) {
	return r.read(ctx)
}

func TestFileReadinessWaitsForInitialization(t *testing.T) {
	for _, state := range []string{"pending", "running"} {
		t.Run(state, func(t *testing.T) {
			reads := 0
			worker := &Worker{stopped: make(chan struct{}), dispatcher: &Dispatcher{
				DeploymentReader: &strictDeploymentReader{t: t, environmentAllocation: func(context.Context, deployment.AllocationKey) (deployment.Allocation, error) {
					return deployment.Allocation{}, deployment.ErrNotFound
				}},
				SessionsReader: initializationReadinessReader{read: func(context.Context) (sessions.Environment, error) {
					reads++
					initialization := state
					if reads > 1 {
						initialization = "complete"
					}
					return sessions.Environment{SessionID: "session", Initialization: initialization}, nil
				}},
			}}
			if err := worker.waitRuntimeAwake(t.Context(), sessions.Environment{SessionID: "session"}); err != nil || reads != 2 {
				t.Fatalf("Files proceeded before initialization: reads=%d err=%v", reads, err)
			}
		})
	}
}

func TestFileReadinessRejectsFailedInitializationAndLostOwnership(t *testing.T) {
	storageError := errors.New("environment lookup failed")
	for _, test := range []struct {
		name, state, session string
		readError, want      error
	}{
		{name: "failed", state: "failed", session: "session", want: ErrExecutionUnavailable},
		{name: "unknown", state: "", session: "session", want: ErrExecutionUnavailable},
		{name: "changed Session", state: "complete", session: "foreign", want: sessions.ErrNotFound},
		{name: "deleted", readError: sessions.ErrNotFound, want: sessions.ErrNotFound},
		{name: "storage", readError: storageError, want: storageError},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker := &Worker{dispatcher: &Dispatcher{SessionsReader: initializationReadinessReader{read: func(context.Context) (sessions.Environment, error) {
				return sessions.Environment{SessionID: test.session, Initialization: test.state}, test.readError
			}}}}
			if err := worker.waitEnvironmentInitialization(t.Context(), sessions.Environment{SessionID: "session"}); !errors.Is(err, test.want) {
				t.Fatal(err)
			}
		})
	}
}

func TestFileReadinessUsesExistingCancellationBudget(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "worker stopped"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
			defer cancel()
			stopped := make(chan struct{})
			worker := &Worker{stopped: stopped, dispatcher: &Dispatcher{SessionsReader: initializationReadinessReader{read: func(context.Context) (sessions.Environment, error) {
				if mode == "cancel" {
					cancel()
				}
				if mode == "worker stopped" {
					close(stopped)
				}
				return sessions.Environment{Initialization: "running"}, nil
			}}}}
			if err := worker.waitEnvironmentInitialization(ctx, sessions.Environment{}); !errors.Is(err, ErrExecutionUnavailable) {
				t.Fatal(err)
			}
		})
	}
}
