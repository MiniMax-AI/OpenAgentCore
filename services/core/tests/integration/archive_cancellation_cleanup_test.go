package integration

import (
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

func TestArchiveCancellationThenDeleteRemovesHome(t *testing.T) {
	h := newDispatchHarnessForSession(t, []byte(hostedLinkSession))
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{HomeRemoval: proto.CapabilitySupported, SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: workerEnvironmentCapabilities()}}})
	if _, err := h.s.pool.Exec(t.Context(), "INSERT INTO execution_project_scopes(tenant_id,organization_id,project_id) VALUES($1,'archive-cleanup',$2)", h.tenant, h.tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.pool.Exec(t.Context(), "INSERT INTO projects(id,name,tenant_id,subject_kind,subject_id) VALUES($1,'Archive cleanup',$1,'service_account',$2)", h.tenant, "project:"+h.tenant); err != nil {
		t.Fatal(err)
	}
	owner := h.owner()
	worker := startOwnedWorker(t, t.Context(), h.s, h.d, owner)
	runWorker(t, worker)
	if _, err := sessionService(t, h.s).ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "active", []sessions.Input{messageInput("run")}); err != nil {
		t.Fatal(err)
	}
	frame := h.read(proto.TypeExecutionPrepare)
	handle := acknowledgePreparation(h, frame.ID)
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	started := h.read(proto.TypeExecutionStart)
	var start proto.ExecutionStartPayload
	if started.DecodePayload(&start) != nil || start.RunID == "" {
		t.Fatal("missing active Turn")
	}
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
	command := sessions.DeleteSessionCommand{TenantID: h.tenant, SessionID: h.session.ID}
	if err := sessionService(t, h.s).DeleteSession(t.Context(), command); !errors.Is(err, sessions.ErrNotIdle) {
		t.Fatal("active Delete did not refuse", err)
	}
	if _, err := owner.Deployment.ArchiveSession(adminDeleteContext(t.Context(), h.tenant, uuid.NewString()), h.tenant, h.session.ID, 1); err != nil {
		t.Fatal(err)
	}
	allocation, err := deploymentStore(h.s).EnvironmentAllocation(t.Context(), deployment.AllocationKey{TenantID: h.tenant, EnvironmentID: h.session.Environment.ID})
	if err != nil || allocation.State != "cleanup_pending" {
		t.Fatal("archive lost cleanup ownership", allocation, err)
	}
	// Provider cleanup and host cancellation proceed independently. Only the host's
	// receipt can settle the Turn, even after the sandbox stops Serving.
	h.stopServing()
	var cancellation proto.PromptCancelPayload
	var release proto.Envelope
	for cancellation.DeliveryID == "" || release.Type == "" {
		var incoming proto.Envelope
		_ = h.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if err := h.conn.ReadJSON(&incoming); err != nil {
			t.Fatal(err)
		}
		h.observe(incoming)
		switch incoming.Type {
		case proto.TypePromptCancel:
			if incoming.DecodePayload(&cancellation) != nil || cancellation.DeliveryID == "" {
				t.Fatal("cancel lost receipt identity")
			}
		case proto.TypeAssignmentRelease:
			var request proto.AssignmentReleasePayload
			if incoming.DecodePayload(&request) != nil || request.RemoveHome {
				t.Fatal("Archive removed the home", request)
			}
			release = incoming
		}
	}
	turn, err := sessionAdapter(h.s).GetTurn(t.Context(), h.tenant, h.session.ID, start.RunID)
	if err != nil || turn.Status != sessions.TurnInProgress || turn.CancelRequestedAt.IsZero() {
		t.Fatal("archive fabricated cancellation settlement", turn, err)
	}
	if err := sessionService(t, h.s).DeleteSession(t.Context(), command); !errors.Is(err, sessions.ErrNotIdle) {
		t.Fatal("Delete accepted cancellation without its receipt", err)
	}
	h.write(start.RunID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: cancellation.DeliveryID, Applied: true, Outcome: &proto.DonePayload{}})
	reply, err := release.Reply(proto.TypeAssignmentStatus, proto.AssignmentStatusPayload{State: proto.AssignmentReleased})
	if err != nil || h.conn.WriteJSON(reply) != nil {
		t.Fatal("cannot acknowledge Archive release", err)
	}
	awaitDaemonRemoteCondition(t, t.Context(), 5*time.Second, "cancellation receipt settled", func() bool {
		turn, err := sessionAdapter(h.s).GetTurn(t.Context(), h.tenant, h.session.ID, start.RunID)
		return err == nil && turn.Status == sessions.TurnCancelled
	})
	if err := sessionService(t, h.s).DeleteSession(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	deleted := h.read(proto.TypeAssignmentRelease)
	var request proto.AssignmentReleasePayload
	if deleted.DecodePayload(&request) != nil || !request.RemoveHome || deleted.Assignment.Epoch <= release.Assignment.Epoch {
		t.Fatal("idle Delete did not request home removal", deleted.Assignment, request)
	}
	reply, err = deleted.Reply(proto.TypeAssignmentStatus, proto.AssignmentStatusPayload{State: proto.AssignmentHomeRemoved})
	if err != nil || h.conn.WriteJSON(reply) != nil {
		t.Fatal("cannot acknowledge home removal", err)
	}
	awaitDaemonRemoteCondition(t, t.Context(), 5*time.Second, "home removal acknowledged", func() bool {
		var applied int64
		err := h.s.pool.QueryRow(t.Context(), "SELECT applied_epoch FROM session_runtime_assignments WHERE session_id=$1", h.session.ID).Scan(&applied)
		return err == nil && applied == int64(deleted.Assignment.Epoch)
	})
	if _, ok, err := sessionAdapter(h.s).GetDeviceCredential(t.Context(), h.device.ID); err != nil || !ok {
		t.Fatal("Session cleanup revoked the shared host", err)
	}
}
