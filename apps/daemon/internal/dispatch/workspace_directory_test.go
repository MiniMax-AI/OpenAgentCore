package dispatch_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
)

func TestWorkspaceDirectoryRetainsEnvironmentAndTransferredOwner(t *testing.T) {
	sender := &recSender{}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(ctx context.Context, _ string, _ proto.MessageInput, out chan<- proto.Envelope) (agent.Session, error) {
		return &fakeSession{out: out, ctx: ctx, closeOutOnCancel: true}, nil
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	for _, name := range []string{"file", "second"} {
		if err := os.WriteFile(filepath.Join(os.Getenv("OAC_RUNTIME_WORKSPACE"), name), []byte("abc"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "prepare", preparationRequest()))
	ready := waitPreparationStatus(t, sender, "prepare", "ready", "")
	request := proto.WorkspaceReadPayload{Operation: "directory", Handle: ready.Handle, EnvironmentID: preparationEnvironmentID, MaxEntries: 1}
	for _, phase := range []string{"idle", "active"} {
		_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, phase, request))
		result := waitWorkspaceRead(t, sender, phase)
		if result.Outcome != "completed" || !result.CloseAcknowledged || result.Directory == nil || !result.Directory.Truncated || len(result.Directory.Entries) != 1 || len(result.Data) != 0 {
			t.Fatal(result)
		}
		bad := request
		bad.EnvironmentID = "another-environment"
		_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, phase+"-foreign", bad))
		if got := waitWorkspaceRead(t, sender, phase+"-foreign"); got.ErrorCode != "resource_unavailable" {
			t.Fatal(got)
		}
		if phase == "idle" {
			_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionStart, "prepare", proto.ExecutionStartPayload{Handle: ready.Handle, ExecutorID: ready.ExecutorID, RunID: "run", Input: proto.TextInput("start")}))
			waitPreparationStatus(t, sender, "prepare", "started", "")
			_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, "stale", request))
			if got := waitWorkspaceRead(t, sender, "stale"); got.Outcome != "rejected" {
				t.Fatal(got)
			}
			request.Handle, request.RunID = "", "run"
		}
	}
	for index, bad := range []proto.WorkspaceReadPayload{
		{Operation: "directory", RunID: "run", EnvironmentID: preparationEnvironmentID, MaxEntries: 2, MaxBytes: 1},
		{Operation: "directory", RunID: "run", EnvironmentID: preparationEnvironmentID, MaxEntries: proto.WorkspaceDirectoryMaxEntries + 1},
		{Operation: "directory", Handle: ready.Handle, RunID: "run", EnvironmentID: preparationEnvironmentID, MaxEntries: 2},
		{Operation: "recursive", RunID: "run", EnvironmentID: preparationEnvironmentID, MaxEntries: 2},
	} {
		id := fmt.Sprintf("invalid-%d", index)
		_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, id, bad))
		if got := waitWorkspaceRead(t, sender, id); got.Outcome != "rejected" || got.ErrorCode != "invalid_request" || got.Directory != nil {
			t.Fatal("malformed directory control reached a resource", got)
		}
	}
}
