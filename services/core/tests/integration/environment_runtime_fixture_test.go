package integration

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// fixtureLinkResource gives the Session's Environment a live Link resource
// and returns it with its Serve credential: for self_hosted, the enrollment of
// a new executor key, whose token Serves; otherwise a running allocation.
func fixtureLinkResource(t *testing.T, s *Store, tenant string, session sessions.Session) (sandboxbootstrap.Resource, []byte) {
	t.Helper()
	environment := session.Environment.ID
	var snapshot struct {
		Environment struct{ Type string } `json:"environment"`
	}
	if err := json.Unmarshal(session.Configuration, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Environment.Type == "self_hosted" {
		key, err := sessionService(t, s).IssueExecutorCredential(t.Context(), FixtureExecutorPrincipal(t, s, tenant), uuid.NewString(), environment)
		if err != nil {
			t.Fatal(err)
		}
		resource, err := sessionService(t, s).EnrollRuntime(t.Context(), environment, runtimedevice.HashCredential(key.Token))
		if err != nil {
			t.Fatal(err)
		}
		return resource, []byte(key.Token)
	}
	resource := sandboxbootstrap.Resource{TenantID: tenant, EnvironmentID: environment, Kind: "allocation", ID: uuid.NewString(), Generation: 1}
	serve := []byte(uuid.NewString())
	insertAllocation(t, s, resource, serve)
	return resource, serve
}

// connectFixtureRuntime connects another agent host with session placed on
// it, and has the Session's Environment Serve a Link resource of its own at
// h's relay.
func connectFixtureRuntime(t *testing.T, h *dispatchHarness, session sessions.Session) *dispatchHarness {
	t.Helper()
	// The Runtime shares the harness's Core, not its connection or write lock.
	other := &dispatchHarness{t: h.t, s: h.s, lease: h.lease, owned: h.owned, d: h.d, tenant: h.tenant, session: session, registry: h.registry, url: h.url,
		admissions: h.admissions, environments: h.environments, link: h.link}
	host := registerAgentHost(t, h.s)
	other.device, other.credential = sessions.ExecutionDevice{ID: host.ID, Name: "agent host"}, host.Credential
	other.resource, other.serve = fixtureLinkResource(t, h.s, h.tenant, session)
	// The placement precedes Serve, so a running Worker cannot place the
	// Session on another connected agent host first.
	assignSession(t, h.s, session.ID, host.ID)
	other.served = startLinkServe(t, h.link, other.serve, other.resource.Ref())
	within(t, other.served.connected)
	u, err := url.Parse(h.url)
	if err != nil {
		t.Fatal(err)
	}
	u.Scheme, u.Path = "ws", "/api/v1/agent-daemon/ws"
	u.RawQuery = url.Values{"device_id": {other.device.ID}, "version": {proto.Version}}.Encode()
	other.conn, _, err = websocket.DefaultDialer.Dial(u.String(), http.Header{"Authorization": {"Bearer " + other.credential}})
	if err != nil {
		t.Fatal("agent host connection failed")
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
	environment, err := sessionAdapter(h.s).GetSessionEnvironment(t.Context(), h.tenant, h.session.ID)
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
	pool := h.s.pool
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM runtime_allocations WHERE environment_id IN (SELECT id FROM environments WHERE session_id=$1)", h.session.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("self-hosted fixture allocated managed compute", count, err)
	}
}

func awaitFixtureCapabilities(t *testing.T, h *dispatchHarness, caps proto.AgentKindCapabilities) {
	t.Helper()
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{HomeRemoval: proto.CapabilityUnsupported, SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: caps}}})
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "updated Runtime capabilities", func() bool {
		peer, err := h.registry.LookupDevice(h.device.ID)
		if err != nil {
			return false
		}
		info, _, known := peer.AgentKindStatus("codex")
		return known && info.Capabilities == caps
	})
}
