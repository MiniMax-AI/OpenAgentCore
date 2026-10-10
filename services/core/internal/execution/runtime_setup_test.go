package execution

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

const setupCanary = "CANARY-runtime-setup-9b3e"

type receiptRuntime struct {
	result  proto.RuntimePrepareResultPayload
	err     error
	request proto.RuntimePreparePayload
	data    []byte
}

func (p *receiptRuntime) PrepareRuntime(_ context.Context, _ string, request proto.RuntimePreparePayload, data []byte) (proto.RuntimePrepareResultPayload, error) {
	p.request, p.data = request, data
	return p.result, p.err
}
func TestRuntimeSetupReceiptOutcomes(t *testing.T) {
	for _, test := range []struct {
		name, outcome string
		code          int
		err           error
		confirmed     bool
	}{
		{"completed", "completed", 0, nil, false}, {"failed", "failed", 3, nil, true}, {"failed without status", "failed", 0, nil, true}, {"maximum status", "failed", 255, nil, true}, {"rejected", "rejected", 0, nil, true}, {"unknown", "unknown", 0, nil, false}, {"transport", "completed", 0, errors.New(setupCanary), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			peer := &receiptRuntime{result: proto.RuntimePrepareResultPayload{Outcome: test.outcome, ExitCode: test.code}, err: test.err}
			err := runRuntimeSetup(initializationTestContext(t), peer, agentcapabilities.Identity{}, runtimeSetupOperation{Request: proto.RuntimePreparePayload{Action: "initialize", Initialization: &proto.RuntimeInitialization{Action: "setup", Command: setupCanary}}})
			if test.outcome == "completed" && test.err == nil {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var failed *runtimeStepFailure
			if err == nil || errors.As(err, &failed) != test.confirmed || strings.Contains(err.Error(), setupCanary) {
				t.Fatal("unsafe outcome", err)
			}
			if failed != nil && failed.exitCode != test.code {
				t.Fatal("exit status lost")
			}
		})
	}
}
func TestRuntimeSetupFailureLabels(t *testing.T) {
	for action, want := range map[string]sessions.ProvisioningFailure{"setup": {Step: sessions.ProvisioningSetupCommand, Index: 2, ExitCode: 3}, "python": {Step: sessions.ProvisioningPythonPackages, Index: 2, ExitCode: 3}, "npm": {Step: sessions.ProvisioningNPMPackages, Index: 2, ExitCode: 3}, "skill": {Step: sessions.ProvisioningSkill, Index: 2, ExitCode: 3}, "configure": {}, "": {}} {
		op := runtimeSetupOperation{Request: proto.RuntimePreparePayload{Action: action}, Index: 2}
		if action != "skill" {
			op.Request = proto.RuntimePreparePayload{Action: "initialize", Initialization: &proto.RuntimeInitialization{Action: action}}
		}
		if got := op.provisioningFailure(3); got != want {
			t.Fatal(action, got)
		}
	}
	operations := setupOperations(environmentconfig.Setup{Commands: []environmentconfig.SetupCommand{{Command: "a"}, {Command: "b"}}})
	if len(operations) != 3 || operations[1].Index != 0 || operations[2].Index != 1 || operations[2].Request.Initialization.CWD != "" {
		t.Fatal("command index or Runtime default changed")
	}
}
func TestInitialFileUsesTypedRuntimeBytes(t *testing.T) {
	body := []byte(setupCanary)
	size := int64(len(body))
	owner := agentcapabilities.Identity{EnvironmentID: "environment", SessionID: "session"}
	peer := &receiptRuntime{result: proto.RuntimePrepareResultPayload{Outcome: "completed"}}
	if err := installInitialFile(initializationTestContext(t), peer, owner, environmentconfig.InitialFileMetadata{Path: "/workspace/a", SizeBytes: &size}, body); err != nil {
		t.Fatal(err)
	}
	if peer.request.Action != "file" || peer.request.File.Path != "/workspace/a" || peer.request.EnvironmentID != owner.EnvironmentID || peer.request.SessionID != owner.SessionID || string(peer.data) != setupCanary {
		t.Fatal("file transport changed")
	}
	size++
	if err := installInitialFile(initializationTestContext(t), peer, owner, environmentconfig.InitialFileMetadata{SizeBytes: &size}, body); err == nil {
		t.Fatal("mismatched source size accepted")
	}
}

func initializationTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func TestRuntimeSetupUsesRemainingInitializationBudget(t *testing.T) {
	ctx := initializationTestContext(t)
	deadline, _ := ctx.Deadline()
	peer := &receiptRuntime{result: proto.RuntimePrepareResultPayload{Outcome: "completed"}}
	err := runRuntimeSetup(ctx, peer, agentcapabilities.Identity{}, runtimeSetupOperation{})
	if err != nil {
		t.Fatal(err)
	}
	remaining := time.Until(deadline).Milliseconds()
	if peer.request.BudgetMS < remaining || peer.request.BudgetMS > remaining+1000 || peer.request.BudgetMS <= 120000 {
		t.Fatalf("remaining operation budget lost: %d vs %d", peer.request.BudgetMS, remaining)
	}
	for name, c := range map[string]context.Context{"unbounded": t.Context(), "expired": func() context.Context {
		c, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		stop()
		return c
	}()} {
		t.Run(name, func(t *testing.T) {
			peer := &receiptRuntime{}
			if runRuntimeSetup(c, peer, agentcapabilities.Identity{}, runtimeSetupOperation{}) == nil || peer.request.BudgetMS != 0 {
				t.Fatal("invalid budget sent")
			}
		})
	}
}
