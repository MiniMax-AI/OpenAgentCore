package integration

import (
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestEnvironmentDirectoryActiveRunUsesReadOnlyPreparation(t *testing.T) {
	h, w, environment := directoryWorker(t)
	awaitFixtureCapabilities(t, h, workerEnvironmentCapabilities())
	pending, err := sessionService(t, h.s).ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "execute", []sessions.Input{messageInput("work")})
	if err != nil {
		t.Fatal(err)
	}
	prepare := h.read(proto.TypeExecutionPrepare)
	handle := acknowledgePreparation(h, prepare.ID)
	h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	frame := h.read(proto.TypeExecutionStart)
	var start proto.ExecutionStartPayload
	if frame.DecodePayload(&start) != nil || start.RunID == "" {
		t.Fatal("execution did not start")
	}
	h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
	result := startDirectoryRead(t.Context(), w, environment)
	readPrepare, read := prepareDirectoryRead(t, h, environment)
	completeDirectoryRead(t, h, readPrepare, read, false, false)
	if got := awaitDirectoryResult(t, result); got.err != nil || len(got.value.Entries) != 1 {
		t.Fatal("active read", got.err)
	}
	h.write(start.RunID, proto.TypeDone, proto.DonePayload{})
	completeEmptyArtifactExport(t, h)
	run := awaitWorkerEnvironmentRun(t, t.Context(), h.s, h.tenant, pending)
	if run.Turn.Status != sessions.TurnCompleted {
		t.Fatal("active read changed Turn outcome")
	}
	assertPreparationReleased(t, h, prepare.ID, handle)
}
