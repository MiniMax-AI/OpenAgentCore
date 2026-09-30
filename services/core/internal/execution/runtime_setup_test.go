package execution

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
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
			err := runRuntimeSetup(t.Context(), peer, agentcapabilities.Identity{}, runtimeSetupOperation{Request: proto.RuntimePreparePayload{Action: "initialize", Initialization: &proto.RuntimeInitialization{Action: "setup", Command: setupCanary}}})
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
	for action, want := range map[string]store.ProvisioningFailure{"setup": {Step: store.ProvisioningSetupCommand, Index: 2, ExitCode: 3}, "python": {Step: store.ProvisioningPythonPackages, Index: 2, ExitCode: 3}, "npm": {Step: store.ProvisioningNPMPackages, Index: 2, ExitCode: 3}, "skill": {Step: store.ProvisioningSkill, Index: 2, ExitCode: 3}, "configure": {}, "": {}} {
		op := runtimeSetupOperation{Request: proto.RuntimePreparePayload{Action: action}, Index: 2}
		if action != "skill" {
			op.Request = proto.RuntimePreparePayload{Action: "initialize", Initialization: &proto.RuntimeInitialization{Action: action}}
		}
		if got := op.provisioningFailure(3); got != want {
			t.Fatal(action, got)
		}
	}
	operations := setupOperations(store.EnvironmentSetup{Commands: []store.SetupCommand{{Command: "a"}, {Command: "b"}}})
	if len(operations) != 3 || operations[1].Index != 0 || operations[2].Index != 1 || operations[2].Request.Initialization.CWD != "" {
		t.Fatal("command index or Runtime default changed")
	}
}
func TestInitialFileUsesTypedRuntimeBytes(t *testing.T) {
	body := []byte(setupCanary)
	size := int64(len(body))
	owner := agentcapabilities.Identity{EnvironmentID: "environment", SessionID: "session"}
	peer := &receiptRuntime{result: proto.RuntimePrepareResultPayload{Outcome: "completed"}}
	if err := installInitialFile(t.Context(), peer, owner, store.InitialFileMetadata{Path: "/workspace/a", SizeBytes: &size}, body); err != nil {
		t.Fatal(err)
	}
	if peer.request.Action != "file" || peer.request.File.Path != "/workspace/a" || peer.request.EnvironmentID != owner.EnvironmentID || peer.request.SessionID != owner.SessionID || string(peer.data) != setupCanary {
		t.Fatal("file transport changed")
	}
	size++
	if err := installInitialFile(t.Context(), peer, owner, store.InitialFileMetadata{SizeBytes: &size}, body); err == nil {
		t.Fatal("mismatched source size accepted")
	}
}
