package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

type localWriteResult struct {
	size int64
	err  error
}

func startLocalWrite(ctx context.Context, w *execution.Worker, e sessions.Environment) <-chan localWriteResult {
	done := make(chan localWriteResult, 1)
	go func() {
		size, err := w.WriteEnvironmentFile(ctx, e, "input", []byte("abc"))
		done <- localWriteResult{size, err}
	}()
	return done
}

func awaitLocalWrite(t *testing.T, done <-chan localWriteResult) localWriteResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("write did not return")
		return localWriteResult{}
	}
}

func TestLocalEnvironmentFileWriteOwnsMutationBeforeDispatch(t *testing.T) {
	h, w, environment := localWorkerForSession(t, false, `{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/home/user/project"}}`)
	foreign := environment
	foreign.TenantID = uuid.NewString()
	if _, err := w.WriteEnvironmentFile(t.Context(), foreign, "input", nil); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("foreign upload", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := startLocalWrite(ctx, w, environment)
	// Environment work without a Turn binds the Session's assignment first.
	bind := h.read(proto.TypeAssignmentBind)
	var binding proto.AssignmentBindPayload
	if bind.DecodePayload(&binding) != nil || bind.Assignment.SessionID != h.session.ID || bind.Assignment.Epoch != 1 || binding.EnvironmentID != environment.ID || binding.WorkspaceDirectory != "/home/user/project" {
		t.Fatal("upload did not bind the Session's assignment")
	}
	h.assignmentFrame(bind)
	begin := h.read(proto.TypeWorkspaceWrite)
	var request proto.WorkspaceWritePayload
	if begin.Assignment != bind.Assignment || begin.DecodePayload(&request) != nil || request.Step != "begin" || request.EnvironmentID != environment.ID || request.SessionID != h.session.ID || request.Path != "input" || request.SizeBytes != 3 {
		t.Fatal("upload identity changed")
	}
	intent, err := FixtureFileWrite(t.Context(), h.s.pool, h.tenant, environment.ID, begin.ID)
	if err != nil || intent.State != "pending" || intent.Identity.DeviceID != h.device.ID {
		t.Fatal("dispatch preceded durable ownership", intent, err)
	}
	if _, err := sessionService(t, h.s).ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "concurrent", []sessions.Input{messageInput("work")}); !errors.Is(err, sessions.ErrTurnConflict) {
		t.Fatal("upload admitted concurrent execution", err)
	}
	cancel()
	if got := awaitLocalWrite(t, done); !errors.Is(got.err, execution.ErrExecutionUnavailable) {
		t.Fatal("detached observer", got.err)
	}
	h.write(begin.ID, proto.TypeWorkspaceWriteResult, proto.WorkspaceWriteResultPayload{Outcome: "ready"})
	chunk := h.read(proto.TypeWorkspaceWrite)
	if chunk.ID != begin.ID || chunk.DecodePayload(&request) != nil || request.Step != "chunk" || string(request.Data) != "abc" {
		t.Fatal("body changed")
	}
	h.write(begin.ID, proto.TypeWorkspaceWriteResult, proto.WorkspaceWriteResultPayload{Outcome: "received", Offset: 3})
	commit := h.read(proto.TypeWorkspaceWrite)
	if commit.ID != begin.ID || commit.DecodePayload(&request) != nil || request.Step != "commit" {
		t.Fatal("commit changed")
	}
	h.write(begin.ID, proto.TypeWorkspaceWriteResult, proto.WorkspaceWriteResultPayload{Outcome: "completed", SizeBytes: 3})
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "detached durable commit", func() bool {
		got, e := FixtureFileWrite(t.Context(), h.s.pool, h.tenant, environment.ID, begin.ID)
		return e == nil && got.State == "committed"
	})
	session, err := sessionAdapter(h.s).GetSession(t.Context(), h.tenant, h.session.ID)
	if err != nil || session.LastTurn != nil {
		t.Fatal("upload created model execution", err)
	}
}

func TestLocalEnvironmentFileWriteLostReceiptRemainsPending(t *testing.T) {
	h, w, environment := localWorker(t, false)
	done := startLocalWrite(t.Context(), w, environment)
	begin := h.read(proto.TypeWorkspaceWrite)
	if err := h.conn.Close(); err != nil {
		t.Fatal(err)
	}
	if result := awaitLocalWrite(t, done); !errors.Is(result.err, execution.ErrExecutionUnavailable) {
		t.Fatal(result.err)
	}
	intent, err := FixtureFileWrite(t.Context(), h.s.pool, h.tenant, environment.ID, begin.ID)
	if err != nil || intent.State != "pending" {
		t.Fatal("disconnect guessed rejection", intent, err)
	}
	if _, err := sessionService(t, h.s).ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "after-loss", []sessions.Input{messageInput("work")}); !errors.Is(err, sessions.ErrTurnConflict) {
		t.Fatal("unknown upload admitted execution", err)
	}
}

func TestLocalEnvironmentFileWriteKnownRejectionReleasesMutation(t *testing.T) {
	h, w, environment := localWorker(t, false)
	for range 2 {
		done := startLocalWrite(t.Context(), w, environment)
		begin := h.read(proto.TypeWorkspaceWrite)
		h.write(begin.ID, proto.TypeWorkspaceWriteResult, proto.WorkspaceWriteResultPayload{Outcome: "rejected", ErrorCode: "resource_unavailable"})
		if result := awaitLocalWrite(t, done); !errors.Is(result.err, execution.ErrExecutionUnavailable) {
			t.Fatal(result.err)
		}
		intent, err := FixtureFileWrite(t.Context(), h.s.pool, h.tenant, environment.ID, begin.ID)
		if err != nil || intent.State != "rejected" {
			t.Fatal("rejection did not settle", intent, err)
		}
	}
}

func TestLocalEnvironmentFileWriteRejectsUnservedEnvironment(t *testing.T) {
	h, w, environment := localWorker(t, false)
	h.stopServing()
	if _, err := w.WriteEnvironmentFile(t.Context(), environment, "input", nil); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("an Environment that is not Serving was written", err)
	}
}
