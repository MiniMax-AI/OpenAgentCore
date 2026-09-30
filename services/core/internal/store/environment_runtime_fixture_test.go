package store_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func enrollFixtureSession(t *testing.T, s *store.Store, tenant string, session sessions.Session) (sessions.ExecutionDevice, string) {
	t.Helper()
	environment, err := s.GetSessionEnvironment(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	principal := store.FixtureExecutorPrincipal(t, s, tenant)
	key, err := s.IssueExecutorCredential(t.Context(), principal, uuid.NewString(), environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := s.EnrollRuntime(t.Context(), environment.ID, runtimedevice.HashCredential(key.Token))
	if err != nil || enrolled.EnvironmentID != environment.ID || enrolled.SessionID != session.ID || enrolled.WorkspaceDirectory != "/workspace" {
		t.Fatalf("Runtime enrollment: %+v %v", enrolled, err)
	}
	bound, err := s.GetSessionDevice(t.Context(), tenant, session.ID)
	if err != nil || bound.ID != enrolled.DeviceID || bound.EnvironmentID != environment.ID {
		t.Fatalf("Runtime binding: %+v %v", bound, err)
	}
	return bound, key.Token
}

func connectFixtureRuntime(t *testing.T, h *dispatchHarness, session sessions.Session) *dispatchHarness {
	t.Helper()
	// The Runtime shares the harness's Core, not its connection or write lock.
	other := &dispatchHarness{t: h.t, s: h.s, db: h.db, lease: h.lease, owned: h.owned, d: h.d, tenant: h.tenant, session: session, registry: h.registry, url: h.url,
		admissions: h.admissions, environments: h.environments}
	other.device, other.credential = enrollFixtureSession(t, h.s, h.tenant, session)
	u, err := url.Parse(h.url)
	if err != nil {
		t.Fatal(err)
	}
	u.Scheme, u.Path = "ws", "/api/v1/agent-daemon/ws"
	u.RawQuery = url.Values{"device_id": {other.device.ID}, "version": {proto.Version}}.Encode()
	other.conn, _, err = websocket.DefaultDialer.Dial(u.String(), http.Header{"Authorization": {"Bearer " + other.credential}})
	if err != nil {
		t.Fatal("enrolled Runtime connection failed")
	}
	t.Cleanup(func() { _ = other.conn.Close() })
	enableWorkerEnvironment(t, other)
	return other
}

func assertPreparationReleased(t *testing.T, h *dispatchHarness, request, handle string) {
	t.Helper()
	frame := h.read(proto.TypeExecutionRelease)
	var release proto.ExecutionReleasePayload
	if frame.ID != request || frame.DecodePayload(&release) != nil || release.Handle != handle {
		t.Fatal("preparation owner was not released", frame.ID, release)
	}
}

// Completed local Turns export their outputs before publishing completion.
func completeEmptyArtifactExport(t *testing.T, h *dispatchHarness, frames ...<-chan proto.Envelope) {
	t.Helper()
	read := h.read
	if len(frames) != 0 {
		read = func(kind string) proto.Envelope { return nextWorkerFrame(t, frames[0], kind) }
	}
	frame := read(proto.TypeExecutionPrepare)
	var prepare proto.ExecutionPreparePayload
	environment, err := h.s.GetSessionEnvironment(t.Context(), h.tenant, h.session.ID)
	if err != nil || frame.DecodePayload(&prepare) != nil || !proto.ValidWorkspaceReadPreparation(prepare.Configuration) || prepare.Configuration.LocalEnvironment == nil || prepare.Configuration.LocalEnvironment.ID != environment.ID {
		t.Fatal("artifact preparation lost exact local authority", err)
	}
	handle := acknowledgePreparation(h, frame.ID)
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	exportFrame := read(proto.TypeWorkspaceExport)
	var request proto.WorkspaceExportPayload
	if exportFrame.DecodePayload(&request) != nil || request.Step != "begin" || request.Handle != handle || request.EnvironmentID != environment.ID {
		t.Fatalf("artifact export changed owner: request=%+v handle=%s environment=%s", request, handle, environment.ID)
	}
	// A closed empty tar is a valid output snapshot.
	h.write(exportFrame.ID, proto.TypeWorkspaceExportResult, proto.WorkspaceExportResultPayload{Outcome: "chunk", Data: make([]byte, 1024)})
	next := read(proto.TypeWorkspaceExport)
	if next.ID != exportFrame.ID || next.DecodePayload(&request) != nil || request.Step != "next" || request.Offset != 1024 {
		t.Fatal("artifact export did not await final receipt")
	}
	h.write(exportFrame.ID, proto.TypeWorkspaceExportResult, proto.WorkspaceExportResultPayload{Outcome: "completed", Offset: 1024})
	releaseFrame := read(proto.TypeExecutionRelease)
	var release proto.ExecutionReleasePayload
	if releaseFrame.ID != frame.ID || releaseFrame.DecodePayload(&release) != nil || release.Handle != handle {
		t.Fatal("artifact preparation was not released")
	}
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "released"})
}

func assertNoRuntimeAllocation(t *testing.T, h *dispatchHarness) {
	t.Helper()
	_, pool := store.NewTestStore(t)
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM runtime_allocations WHERE environment_id IN (SELECT id FROM environments WHERE session_id=$1)", h.session.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("self-hosted fixture allocated managed compute", count, err)
	}
}

func awaitFixtureCapabilities(t *testing.T, h *dispatchHarness, caps proto.AgentKindCapabilities) {
	t.Helper()
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: caps}}})
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "updated Runtime capabilities", func() bool {
		peer, err := h.registry.LookupDevice(h.device.ID)
		if err != nil {
			return false
		}
		info, _, known := peer.AgentKindStatus("codex")
		return known && info.Capabilities.Preparation == caps.Preparation.IsSupported() &&
			info.Capabilities.LocalEnvironment == caps.LocalEnvironment.IsSupported() &&
			info.Capabilities.DurableInputReceipts == caps.DurableInputReceipts.IsSupported() &&
			info.Capabilities.WorkspaceOutputExport == caps.WorkspaceOutputExport.IsSupported()
	})
}
