package execution

import (
	"bytes"
	"context"
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentplugin"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
	"testing"
)

type capabilityFixture struct {
	request   proto.RuntimePreparePayload
	body      []byte
	requestID string
	outcome   string
	err       error
}

func (f *capabilityFixture) PrepareRuntime(_ context.Context, id string, request proto.RuntimePreparePayload, body []byte) (proto.RuntimePrepareResultPayload, error) {
	f.requestID, f.request, f.body = id, request, append([]byte(nil), body...)
	return proto.RuntimePrepareResultPayload{Outcome: f.outcome, ErrorCode: "safe_failure"}, f.err
}
func TestRuntimeCapabilitiesPreserveRawBundlesAndSetupOrdering(t *testing.T) {
	archive := []byte("opaque archive bytes must be expanded only by Runtime")
	setup := store.EnvironmentSetup{
		Skills:                []store.EnvironmentSkill{{Metadata: store.EnvironmentSkillMetadata{Type: "skill_reference", SkillID: "private-reference", Version: "1", Name: "example", Description: "Safe description"}, Archive: archive}},
		Plugins:               []store.EnvironmentPlugin{{Metadata: agentplugin.Metadata{Type: "inline", Name: "plugin", Description: "Safe plugin"}, Archive: archive}},
		Commands:              []store.SetupCommand{{Command: "prepare directory"}},
		CapabilityDirectories: []string{"/workspace/generated"},
	}
	operations := setupOperations(setup)
	if len(operations) != 5 || operations[0].Request.Initialization.Action != "configure" || operations[1].Request.Action != "skill" || operations[2].Request.Action != "plugin" || operations[3].Request.Initialization.Action != "setup" || operations[4].Request.Action != "finalize" {
		t.Fatal("bundle-before-setup or directory-after-setup ordering changed", operations)
	}
	owner := agentcapabilities.Identity{EnvironmentID: uuid.NewString(), SessionID: uuid.NewString()}
	for _, op := range operations {
		peer := &capabilityFixture{outcome: "completed"}
		if err := runRuntimeSetup(t.Context(), peer, owner, op); err != nil {
			t.Fatal(err)
		}
		if _, err := uuid.Parse(peer.requestID); err != nil {
			t.Fatal("request ID", err)
		}
		if peer.request.EnvironmentID != owner.EnvironmentID || peer.request.SessionID != owner.SessionID {
			t.Fatal("allocation identity lost")
		}
		if (op.Request.Action == "skill" || op.Request.Action == "plugin") && !bytes.Equal(peer.body, archive) {
			t.Fatal("Core expanded or changed archive")
		}
		if op.Request.Action == "skill" && (peer.request.Skill.Type != "inline" || peer.request.Skill.Name != "example") {
			t.Fatal("unsafe or unresolved metadata")
		}
		if op.Request.Action == "finalize" && (len(peer.body) != 0 || peer.request.Sources == nil || len(peer.request.Sources.Skills) != 1 || len(peer.request.Sources.Plugins) != 1 || peer.request.Sources.Directories[0] != "/workspace/generated") {
			t.Fatal("finalization selections changed")
		}

	}
}
func TestRuntimeCapabilitiesConfirmedAndUnknownFailures(t *testing.T) {
	for _, outcome := range []string{"failed", "rejected", "unknown", "unexpected"} {
		peer := &capabilityFixture{outcome: outcome}
		err := runRuntimeSetup(t.Context(), peer, agentcapabilities.Identity{}, runtimeSetupOperation{Request: proto.RuntimePreparePayload{Action: "finalize"}})
		var confirmed *runtimeStepFailure
		if err == nil || errors.As(err, &confirmed) != (outcome == "failed" || outcome == "rejected") {
			t.Fatal(outcome, err)
		}
	}
	peer := &capabilityFixture{outcome: "completed", err: errors.New(setupCanary)}
	err := runRuntimeSetup(t.Context(), peer, agentcapabilities.Identity{}, runtimeSetupOperation{Request: proto.RuntimePreparePayload{}})
	if err == nil || bytes.Contains([]byte(err.Error()), []byte(setupCanary)) {
		t.Fatal("transport error leaked or succeeded", err)
	}
}
