package execution

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

type candidateQueryObserver struct {
	sessions.Reader
	scans atomic.Int32
}

func (o *candidateQueryObserver) ListExecutionWork(context.Context, string, []string, []string) ([]sessions.ExecutionWork, error) {
	return nil, nil
}
func (o *candidateQueryObserver) ListEnvironmentInputWork(context.Context, string, []string) ([]sessions.EnvironmentInputWork, error) {
	o.scans.Add(1)
	return nil, nil
}

func TestSchedulingHintScansBeforeDeadlineAndRestoresPollingDelay(t *testing.T) {
	observer := &candidateQueryObserver{}
	worker := &Worker{dispatcher: &Dispatcher{SessionsReader: observer}, concurrency: 1}
	schedule := workerSchedule{nextEnvironmentScan: time.Now().Add(time.Hour)}
	for _, hinted := range []bool{false, true, false} {
		if _, err := schedule.selectWork(t.Context(), worker, nil, map[string]bool{}, hinted); err != nil {
			t.Fatal(err)
		}
	}
	if observer.scans.Load() != 1 {
		t.Fatalf("candidate scans = %d; hint must bypass the deadline once", observer.scans.Load())
	}
}

type phaseSessionReader struct{ sessions.Reader }

func (phaseSessionReader) GetSessionEnvironment(context.Context, string, string) (sessions.Environment, error) {
	return sessions.Environment{ID: "environment", Configuration: json.RawMessage(`{"type":"openai_hosted"}`)}, nil
}
func (phaseSessionReader) GetSessionDevice(context.Context, string, string) (sessions.ExecutionDevice, error) {
	panic("non-running compute must not reach device eligibility")
}
func TestCapabilityHintDoesNotBypassComputePhase(t *testing.T) {
	for _, phase := range []string{"quiescing", "suspending", "suspended", "restoring", "waking"} {
		t.Run(phase, func(t *testing.T) {
			reader := &strictDeploymentReader{t: t, environmentAllocation: func(context.Context, deployment.AllocationKey) (deployment.Allocation, error) {
				return deployment.Allocation{ComputePhase: phase}, nil
			}}
			worker := &Worker{dispatcher: &Dispatcher{SessionsReader: phaseSessionReader{}, DeploymentReader: reader}}
			session := sessions.Session{Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`)}
			ready, err := worker.bindSessionDevice(t.Context(), session, func(string) bool { t.Fatal("phase bypassed"); return true })
			if err != nil || ready {
				t.Fatal("non-running compute selected", ready, err)
			}
		})
	}
}
