package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto/prototest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func localWorker(t *testing.T, execute bool) (*dispatchHarness, *execution.Worker, sessions.Environment) {
	t.Helper()
	return localWorkerForSession(t, execute, `{"agent":{"model":"test-model"},"environment":{"type":"openai_hosted","network":{"access":"disabled"}}}`)
}

func localWorkerForSession(t *testing.T, execute bool, configuration string) (*dispatchHarness, *execution.Worker, sessions.Environment) {
	t.Helper()
	h := newDispatchHarnessForSession(t, []byte(configuration))
	environment, err := sessionAdapter(h.s).GetSessionEnvironment(t.Context(), h.tenant, h.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	caps := prototest.Capabilities(proto.AgentKindCapabilities{LocalEnvironment: proto.CapabilitySupported})
	if execute {
		caps.TextVerbosity = proto.CapabilitySupported
	}
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{HomeRemoval: proto.CapabilityUnsupported, SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: caps}}})
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "local capability", func() bool {
		peer, err := h.registry.LookupDevice(h.device.ID)
		if err != nil {
			return false
		}
		info, _, _ := peer.AgentKindStatus("codex")
		return info.Capabilities.LocalEnvironment.IsSupported()
	})
	w := startWorker(t, t.Context(), h.s, h.d)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("local worker did not stop")
		}
	})
	return h, w, environment
}

func TestLocalEnvironmentWorkerDirectoryUsesExactAuthorityWithoutModel(t *testing.T) {
	h, w, environment := localWorker(t, false)
	foreign := environment
	foreign.TenantID = uuid.NewString()
	if _, err := w.ReadEnvironmentDirectory(t.Context(), foreign, "reports"); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("foreign read admitted", err)
	}
	result := startDirectoryRead(t.Context(), w, environment)
	frame := h.read(proto.TypeExecutionPrepare)
	var prepare proto.ExecutionPreparePayload
	if frame.DecodePayload(&prepare) != nil || !proto.ValidWorkspaceReadPreparation(prepare.Configuration) || prepare.Configuration.LocalEnvironment == nil || prepare.Configuration.LocalEnvironment.ID != environment.ID {
		t.Fatal("local read did not preserve its exact identity")
	}
	handle := acknowledgePreparation(h, frame.ID)
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	read := h.read(proto.TypeWorkspaceRead)
	var input proto.WorkspaceReadPayload
	if read.DecodePayload(&input) != nil || input.EnvironmentID != environment.ID || input.Handle != handle {
		t.Fatal("local directory owner changed")
	}
	completeDirectoryRead(t, h, frame.ID, read.ID, false, false)
	if got := awaitDirectoryResult(t, result); got.err != nil || len(got.value.Entries) != 1 {
		t.Fatal("local directory failed", got.err)
	}
	session, err := sessionAdapter(h.s).GetSession(t.Context(), h.tenant, h.session.ID)
	if err != nil || session.LastTurn != nil || session.EnvironmentInputActivity != nil {
		t.Fatal("local read admitted execution", err)
	}
}

// An Environment whose Link resource is not Serving, or that has none, has no
// directory to read.
func TestLocalEnvironmentWorkerRejectsUnservedEnvironment(t *testing.T) {
	h, w, environment := localWorker(t, false)
	h.stopServing()
	if _, err := w.ReadEnvironmentDirectory(t.Context(), environment, "reports"); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("an Environment that is not Serving was read", err)
	}
	other, err := h.s.CreateSession(t.Context(), h.tenant, sessions.CreateSession{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "unassigned", Configuration: h.session.Configuration})
	if err != nil {
		t.Fatal(err)
	}
	unassigned, err := sessionAdapter(h.s).GetSessionEnvironment(t.Context(), h.tenant, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.ReadEnvironmentDirectory(t.Context(), unassigned, "reports"); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("an Environment without a Link resource was read", err)
	}
	if _, err := sessionAdapter(h.s).GetSessionDevice(t.Context(), h.tenant, other.ID); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("read persisted an unauthorized placement", err)
	}
}

func TestLocalEnvironmentWorkerSchedulesPreparationWithoutRemoteResolver(t *testing.T) {
	h, worker, environment := localWorker(t, true)
	reservation, err := sessionService(t, h.s).ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "local-input", []sessions.Input{messageInput("first")})
	if err != nil {
		t.Fatal(err)
	}
	// The scheduler's scan interval is five seconds.
	_ = h.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var frame proto.Envelope
	for frame.Type != proto.TypeExecutionPrepare {
		if h.conn.ReadJSON(&frame) != nil {
			t.Fatal("local reservation was not scheduled")
		}
		h.assignmentFrame(frame)
	}
	var prepare proto.ExecutionPreparePayload
	if frame.DecodePayload(&prepare) != nil || prepare.Configuration.LocalEnvironment == nil || prepare.Configuration.LocalEnvironment.ID != environment.ID {
		t.Fatal("local preparation lost identity")
	}
	before, err := sessionAdapter(h.s).GetSession(t.Context(), h.tenant, h.session.ID)
	if err != nil || before.LastTurn != nil {
		t.Fatal("preparation admitted execution before readiness", err)
	}
	handle := acknowledgePreparation(h, frame.ID)
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	startFrame := h.read(proto.TypeExecutionStart)
	var start proto.ExecutionStartPayload
	if startFrame.DecodePayload(&start) != nil || start.Handle != handle || start.RunID == "" || inputTextForTest(t, start.Input) != "first" {
		t.Fatal("local Start changed reservation identity")
	}
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
	h.write(start.RunID, proto.TypeDone, proto.DonePayload{Metadata: map[string]any{proto.DoneMetaAgentSessionID: "local-native-history"}})
	completeLocalArtifactExport(t, h, worker, environment)
	awaitDaemonRemoteCondition(t, t.Context(), 5*time.Second, "local completion", func() bool {
		turn, err := sessionAdapter(h.s).GetTurn(t.Context(), h.tenant, h.session.ID, start.RunID)
		return err == nil && turn.Status == sessions.TurnCompleted
	})
	settled, err := sessionAdapter(h.s).GetEnvironmentInputReservation(t.Context(), h.tenant, h.session.ID, reservation.ID)
	if err != nil || settled.State != sessions.EnvironmentInputAdmitted || len(settled.Receipts) != 1 {
		t.Fatal("local reservation did not settle", err)
	}
	bound, err := sessionAdapter(h.s).GetSessionExecutionBinding(t.Context(), h.tenant, h.session.ID)
	if err != nil || bound.Device.ID != h.device.ID || bound.NativeSessionID != "local-native-history" {
		t.Fatal("local native identity was not retained", err)
	}
	artifacts, err := sessionAdapter(h.s).ListSessionArtifacts(t.Context(), h.tenant, h.session.ID, environment.ID, "", 20, false)
	if err != nil || len(artifacts.Artifacts) != 1 || artifacts.Artifacts[0].Path != "/workspace/outputs/result.bin" || artifacts.Artifacts[0].TurnID != start.RunID || artifacts.Artifacts[0].SizeBytes != 3 {
		t.Fatalf("completed turn did not publish output: %+v %v", artifacts, err)
	}
}
