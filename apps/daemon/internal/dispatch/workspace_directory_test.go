package dispatch_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestWorkspaceDirectoryUsesReadOnlyOwnerBesideNativeExecution(t *testing.T) {
	sender := &recSender{}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(ctx context.Context, _ string, _ proto.MessageInput, out chan<- proto.Envelope) (fixtureSession, error) {
		return &fakeSession{out: out, ctx: ctx, closeOutOnCancel: true}, nil
	}
	owner := newTestOwner(preparationEnvironmentID, preparationSessionID)
	owner.put("file", []byte("abc"))
	owner.put("second", []byte("abc"))
	r := ownedPreparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (preparedFixture, error) { return p, nil }, owner)
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "prepare", preparationRequest()))
	ready := waitPreparationStatus(t, sender, "prepare", "ready", "")
	configuration := preparationRequest()
	configuration.Configuration = proto.PromptRequestPayload{AgentKind: "prepared", LocalEnvironment: &proto.LocalEnvironment{ID: preparationEnvironmentID}, WorkspaceReadOnly: true}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "read-prepare", configuration)); err != nil {
		t.Fatal(err)
	}
	readReady := waitPreparationStatus(t, sender, "read-prepare", "ready", "")
	request := proto.WorkspaceReadPayload{Handle: readReady.Handle, EnvironmentID: preparationEnvironmentID, MaxEntries: 1}
	for _, phase := range []string{"idle", "active"} {
		_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, phase, request))
		result := waitWorkspaceRead(t, sender, phase)
		if result.Outcome != "completed" || !result.CloseAcknowledged || result.Directory == nil || !result.Directory.Truncated || len(result.Directory.Entries) != 1 {
			t.Fatal(result)
		}
		bad := request
		bad.EnvironmentID = "another-environment"
		_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, phase+"-foreign", bad))
		if got := waitWorkspaceRead(t, sender, phase+"-foreign"); got.ErrorCode != proto.AssignmentConflict {
			t.Fatal(got)
		}
		native := request
		native.Handle = ready.Handle
		_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, phase+"-native", native))
		if got := waitWorkspaceRead(t, sender, phase+"-native"); got.ErrorCode != "resource_unavailable" {
			t.Fatal("execution preparation admitted a File read", got)
		}
		if phase == "idle" {
			_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionStart, "prepare", proto.ExecutionStartPayload{Handle: ready.Handle, ExecutorID: ready.ExecutorID, RunID: "run", Input: proto.TextInput("start")}))
			waitPreparationStatus(t, sender, "prepare", "started", "")
		}
	}
	for index, bad := range []any{
		proto.WorkspaceReadPayload{Handle: readReady.Handle, EnvironmentID: preparationEnvironmentID, MaxEntries: proto.WorkspaceDirectoryMaxEntries + 1},
		proto.WorkspaceReadPayload{EnvironmentID: preparationEnvironmentID, MaxEntries: 2},
		map[string]any{"run_id": "run", "environment_id": preparationEnvironmentID, "max_entries": 2},
		map[string]any{"handle": readReady.Handle, "run_id": "run", "environment_id": preparationEnvironmentID, "max_entries": 2},
	} {
		id := fmt.Sprintf("invalid-%d", index)
		_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, id, bad))
		if got := waitWorkspaceRead(t, sender, id); got.Outcome != "rejected" || got.ErrorCode != "invalid_request" || got.Directory != nil {
			t.Fatal("malformed directory control reached a resource", got)
		}
	}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionRelease, "read-prepare", proto.ExecutionReleasePayload{Handle: readReady.Handle}))
	waitPreparationStatus(t, sender, "read-prepare", "released", "")
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, "released-read", request))
	if got := waitWorkspaceRead(t, sender, "released-read"); got.ErrorCode != "resource_unavailable" {
		t.Fatal("released read owner remained usable", got)
	}
}
