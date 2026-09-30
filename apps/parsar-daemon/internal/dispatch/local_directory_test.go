package dispatch_test

import "github.com/MiniMax-AI-Dev/parsar/internal/harnessconfig"

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto/prototest"
	"github.com/google/uuid"
)

func TestLocalDirectoryPreparationNeedsNoHarnessAndRejectsOtherOwners(t *testing.T) {
	workspace := t.TempDir()

	environment, session := uuid.NewString(), uuid.NewString()
	binding, err := localworkspace.New(environment, session, workspace)
	if err != nil {
		t.Fatal(err)
	}
	var harnessCalls atomic.Int32
	reg := agent.NewRegistry()
	reg.RegisterKind(proto.SupportedAgentKind{Kind: "native", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{LocalEnvironment: proto.CapabilitySupported})}, harnessconfig.Configuration{}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
		harnessCalls.Add(1)
		return nil, errors.New("must not start a model")
	})
	reg.RegisterPreparation("native", true, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
		harnessCalls.Add(1)
		return nil, errors.New("must not prepare a harness")
	})
	sender := &recSender{}
	r, err := dispatch.New(dispatch.Config{Registry: reg, Sender: sender, LocalWorkspace: binding})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	request := proto.PromptRequestPayload{AgentKind: "native", LocalEnvironment: &proto.LocalEnvironment{ID: environment}, AgentStateKey: "agents-api-" + session, StrictResume: true, ReleaseOnCompletion: true, WorkspaceReadOnly: true}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "idle", proto.ExecutionPreparePayload{Configuration: request})); err != nil {
		t.Fatal(err)
	}
	ready := waitPreparationStatus(t, sender, "idle", "ready", "")
	read := proto.WorkspaceReadPayload{EnvironmentID: environment, Handle: ready.Handle, Operation: "directory", MaxEntries: 10}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, "list", read))
	if got := waitWorkspaceRead(t, sender, "list"); got.Outcome != "completed" || got.Directory == nil {
		t.Fatal("idle directory unavailable", got)
	}
	if err := os.WriteFile(filepath.Join(workspace, "bytes"), []byte{0, 255, 17}, 0600); err != nil {
		t.Fatal(err)
	}
	content := proto.WorkspaceReadPayload{EnvironmentID: environment, Handle: ready.Handle, Path: "bytes", MaxBytes: 2}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, "content", content))
	if got := waitWorkspaceRead(t, sender, "content"); got.Outcome != "completed" || !got.Truncated || !got.CloseAcknowledged || len(got.Data) != 2 || got.Data[1] != 255 {
		t.Fatal("file prefix unavailable", got)
	}
	read.EnvironmentID = uuid.NewString()
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, "foreign", read))
	if got := waitWorkspaceRead(t, sender, "foreign"); got.Outcome != "rejected" {
		t.Fatal("foreign directory accepted", got)
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionStart, "idle", proto.ExecutionStartPayload{Handle: ready.Handle, ExecutorID: ready.ExecutorID, RunID: "forbidden", Input: proto.TextInput("work")})); err == nil {
		t.Fatal("read preparation admitted execution")
	}
	bad := request
	bad.AgentStateKey = "agents-api-" + uuid.NewString()
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "wrong-session", proto.ExecutionPreparePayload{Configuration: bad})); err == nil {
		t.Fatal("wrong Session accepted")
	}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionRelease, "idle", proto.ExecutionReleasePayload{Handle: ready.Handle}))
	waitPreparationStatus(t, sender, "idle", "released", "")
	if harnessCalls.Load() != 0 || r.ActiveRuns() != 0 {
		t.Fatal("read-only operation reached native execution")
	}
}

func waitWorkspaceRead(t *testing.T, sender *recSender, id string) proto.WorkspaceReadResultPayload {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, env := range sender.snapshot() {
			if env.Type == proto.TypeWorkspaceReadResult && env.ID == id {
				var result proto.WorkspaceReadResultPayload
				if env.DecodePayload(&result) != nil {
					t.Fatal("invalid read result")
				}
				return result
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("read result missing", id)
	return proto.WorkspaceReadResultPayload{}
}

func TestLocalDirectoryKeepsNotDirectorySeparateFromFailures(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	environment, session := uuid.NewString(), uuid.NewString()
	binding, err := localworkspace.New(environment, session, workspace)
	if err != nil {
		t.Fatal(err)
	}
	reg := agent.NewRegistry()
	reg.RegisterKind(proto.SupportedAgentKind{Kind: "native", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{LocalEnvironment: proto.CapabilitySupported})}, harnessconfig.Configuration{}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
		return nil, errors.New("must not start a model")
	})
	reg.RegisterPreparation("native", true, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
		return nil, errors.New("must not prepare a harness")
	})
	sender := &recSender{}
	r, err := dispatch.New(dispatch.Config{Registry: reg, Sender: sender, LocalWorkspace: binding})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	request := proto.PromptRequestPayload{AgentKind: "native", LocalEnvironment: &proto.LocalEnvironment{ID: environment}, AgentStateKey: "agents-api-" + session, StrictResume: true, ReleaseOnCompletion: true, WorkspaceReadOnly: true}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "idle", proto.ExecutionPreparePayload{Configuration: request})); err != nil {
		t.Fatal(err)
	}
	ready := waitPreparationStatus(t, sender, "idle", "ready", "")
	for path, want := range map[string]proto.WorkspaceReadResultPayload{
		"missing":    {Outcome: "rejected", ErrorCode: proto.WorkspaceReadNotDirectory},
		"file":       {Outcome: "rejected", ErrorCode: proto.WorkspaceReadNotDirectory},
		"../invalid": {Outcome: "rejected", ErrorCode: "invalid_request"},
	} {
		read := proto.WorkspaceReadPayload{EnvironmentID: environment, Handle: ready.Handle, Operation: "directory", Path: path, MaxEntries: 10}
		_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, path, read))
		if got := waitWorkspaceRead(t, sender, path); got.Outcome != want.Outcome || got.ErrorCode != want.ErrorCode || got.Directory != nil || got.CloseAcknowledged {
			t.Fatal("native directory result changed", path, got)
		}
	}
}
