package store_test

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

type directoryResult struct {
	value proto.WorkspaceDirectoryResult
	err   error
}

func directoryWorker(t *testing.T) (*dispatchHarness, *execution.Worker, sessions.Environment) {
	t.Helper()
	h := newDispatchHarnessForSession(t, []byte(`{"agent":{"model":"unavailable-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`), true)
	environment, err := h.s.GetSessionEnvironment(t.Context(), h.tenant, h.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: prototest.Capabilities(proto.AgentKindCapabilities{LocalEnvironment: proto.CapabilitySupported, Preparation: proto.CapabilitySupported, WorkspaceReadPreparation: proto.CapabilitySupported})}}})
	peer, err := h.registry.LookupDevice(h.device.ID)
	if err != nil {
		t.Fatal(err)
	}
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "read preparation capability", func() bool {
		info, _, _ := peer.AgentKindStatus("codex")
		return info.Capabilities.WorkspaceReadPreparation
	})
	w := startWorker(t, t.Context(), h.db, h.d)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("reader worker did not stop")
		}
	})
	return h, w, environment
}

func startDirectoryRead(ctx context.Context, w *execution.Worker, environment sessions.Environment) <-chan directoryResult {
	ch := make(chan directoryResult, 1)
	go func() {
		value, err := w.ReadEnvironmentDirectory(ctx, environment, "reports")
		ch <- directoryResult{value, err}
	}()
	return ch
}

func awaitDirectoryResult(t *testing.T, ch <-chan directoryResult) directoryResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("directory read did not return")
		return directoryResult{}
	}
}

func prepareDirectoryRead(t *testing.T, h *dispatchHarness, environment sessions.Environment) (string, string) {
	t.Helper()
	frame := h.read(proto.TypeExecutionPrepare)
	var request proto.ExecutionPreparePayload
	if frame.DecodePayload(&request) != nil || !proto.ValidWorkspaceReadPreparation(request.Configuration) || request.Configuration.LocalEnvironment == nil || request.Configuration.LocalEnvironment.ID != environment.ID || request.Configuration.AgentStateKey != "agents-api-"+h.session.ID {
		t.Fatal("read did not use the closed preparation profile")
	}
	handle := acknowledgePreparation(h, frame.ID)
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	read := h.read(proto.TypeWorkspaceRead)
	var input proto.WorkspaceReadPayload
	if read.DecodePayload(&input) != nil || input.Handle != handle || input.RunID != "" || input.EnvironmentID != environment.ID || input.Path != "reports" || input.Operation != "directory" {
		t.Fatal("directory request changed binding or path")
	}
	return frame.ID, read.ID
}

func completeDirectoryRead(t *testing.T, h *dispatchHarness, request, read string, truncated, cleanupFailed bool) {
	t.Helper()
	size := int64(9)
	h.write(read, proto.TypeWorkspaceReadResult, proto.WorkspaceReadResultPayload{Outcome: "completed", CloseAcknowledged: true, Directory: &proto.WorkspaceDirectoryResult{Entries: []proto.WorkspaceDirectoryEntry{{Name: "report.txt", Kind: "file", SizeBytes: &size}}, Truncated: truncated}})
	release := h.read(proto.TypeExecutionRelease)
	var input proto.ExecutionReleasePayload
	if release.ID != request || release.DecodePayload(&input) != nil || input.Handle == "" {
		t.Fatal("reader did not release its preparation")
	}
	status := proto.PreparationStatusPayload{Handle: input.Handle, Revision: 3, State: "released"}
	if cleanupFailed {
		status.State, status.ErrorCode = "failed", "cleanup_unconfirmed"
	}
	h.write(request, proto.TypePreparationStatus, status)
}

func TestEnvironmentDirectoryWorkerReadsWithoutExecutionPrerequisites(t *testing.T) {
	h, w, environment := directoryWorker(t)
	foreign := environment
	foreign.TenantID = uuid.NewString()
	if _, err := w.ReadEnvironmentDirectory(t.Context(), foreign, "reports"); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("foreign reader admitted", err)
	}
	wrong := environment
	wrong.SessionID = uuid.NewString()
	if _, err := w.ReadEnvironmentDirectory(t.Context(), wrong, "reports"); !errors.Is(err, sessions.ErrNotFound) {
		t.Fatal("wrong Session admitted", err)
	}
	result := startDirectoryRead(t.Context(), w, environment)
	request, read := prepareDirectoryRead(t, h, environment)
	if _, err := w.ReadEnvironmentDirectory(t.Context(), environment, "reports"); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("second idle reader bypassed Session owner", err)
	}
	select {
	case <-result:
		t.Fatal("read returned before settlement")
	default:
	}
	completeDirectoryRead(t, h, request, read, false, false)
	got := awaitDirectoryResult(t, result)
	if got.err != nil || len(got.value.Entries) != 1 {
		t.Fatal("directory result", got.err)
	}
	session, err := h.s.GetSession(t.Context(), h.tenant, h.session.ID)
	if err != nil || session.LastTurn != nil || session.EnvironmentInputActivity != nil {
		t.Fatal("directory read manufactured execution")
	}
	bound, err := h.s.GetSessionExecutionBinding(t.Context(), h.tenant, h.session.ID)
	if err != nil || bound.Device.ID != h.device.ID || bound.NativeSessionID != "" {
		t.Fatal("directory read changed native history identity")
	}
}

func TestEnvironmentDirectoryWorkerRejectsIncompleteOrUnreleasedResults(t *testing.T) {
	for _, mode := range []string{"truncated", "cleanup_failed"} {
		t.Run(mode, func(t *testing.T) {
			h, w, environment := directoryWorker(t)
			result := startDirectoryRead(t.Context(), w, environment)
			request, read := prepareDirectoryRead(t, h, environment)
			completeDirectoryRead(t, h, request, read, mode == "truncated", mode == "cleanup_failed")
			got := awaitDirectoryResult(t, result)
			if !errors.Is(got.err, execution.ErrExecutionUnavailable) || len(got.value.Entries) != 0 {
				t.Fatal("incomplete reader exposed data or retained authority", got.err)
			}
		})
	}
}

func rejectDirectoryRead(t *testing.T, h *dispatchHarness, request, read, code string, cleanupFailed bool) {
	t.Helper()
	h.write(read, proto.TypeWorkspaceReadResult, proto.WorkspaceReadResultPayload{Outcome: "rejected", ErrorCode: code})
	release := h.read(proto.TypeExecutionRelease)
	var input proto.ExecutionReleasePayload
	if release.ID != request || release.DecodePayload(&input) != nil || input.Handle == "" {
		t.Fatal("reader did not release its preparation")
	}
	status := proto.PreparationStatusPayload{Handle: input.Handle, Revision: 3, State: "released"}
	if cleanupFailed {
		status.State, status.ErrorCode = "failed", "cleanup_unconfirmed"
	}
	h.write(request, proto.TypePreparationStatus, status)
}

// A path that names no directory lists nothing only after confirmed release;
// every other native rejection keeps its existing error.
func TestEnvironmentDirectoryNotDirectoryIsAnEmptyListing(t *testing.T) {
	for _, test := range []struct {
		code          string
		cleanupFailed bool
		err           error
	}{
		{proto.WorkspaceReadNotDirectory, false, nil},
		{proto.WorkspaceReadNotDirectory, true, execution.ErrExecutionUnavailable},
		{"not_found", false, sessions.ErrNotFound},
		{"invalid_request", false, execution.ErrExecutionUnavailable},
		{"permission_denied", false, execution.ErrExecutionUnavailable},
		{"resource_unavailable", false, execution.ErrExecutionUnavailable},
	} {
		t.Run(test.code, func(t *testing.T) {
			h, w, environment := directoryWorker(t)
			foreign := environment
			foreign.TenantID = uuid.NewString()
			if _, err := w.ReadEnvironmentDirectory(t.Context(), foreign, "reports"); !errors.Is(err, sessions.ErrNotFound) {
				t.Fatal("foreign reader admitted", err)
			}
			result := startDirectoryRead(t.Context(), w, environment)
			request, read := prepareDirectoryRead(t, h, environment)
			rejectDirectoryRead(t, h, request, read, test.code, test.cleanupFailed)
			got := awaitDirectoryResult(t, result)
			if test.err == nil {
				if got.err != nil || got.value.Entries == nil || len(got.value.Entries) != 0 || got.value.Truncated {
					t.Fatal("not-directory result was not an empty listing", got.err, got.value)
				}
				return
			}
			if !errors.Is(got.err, test.err) || len(got.value.Entries) != 0 {
				t.Fatal("native rejection changed its error", got.err)
			}
		})
	}
}

func TestEnvironmentDirectorySequentialReadsReleaseSchedulingOwnership(t *testing.T) {
	h, w, environment := directoryWorker(t)
	const pages = 32
	done := make(chan error, 1)
	go func() {
		for range pages {
			value, err := w.ReadEnvironmentDirectory(t.Context(), environment, "reports")
			if err != nil {
				done <- err
				return
			}
			if len(value.Entries) != 1 {
				done <- errors.New("missing directory page")
				return
			}
		}
		done <- nil
	}()
	for range pages {
		request, read := prepareDirectoryRead(t, h, environment)
		completeDirectoryRead(t, h, request, read, false, false)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("sequential reads retained ownership", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sequential reads did not finish")
	}
}

func TestEnvironmentDirectoryObserverCancellationRetainsReadOwner(t *testing.T) {
	h, w, environment := directoryWorker(t)
	ctx, cancel := context.WithCancel(t.Context())
	result := startDirectoryRead(ctx, w, environment)
	request, read := prepareDirectoryRead(t, h, environment)
	cancel()
	if got := awaitDirectoryResult(t, result); !errors.Is(got.err, execution.ErrExecutionUnavailable) {
		t.Fatal("cancelled observer result", got.err)
	}
	if _, err := w.ReadEnvironmentDirectory(t.Context(), environment, "reports"); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("cancelled observer freed Session owner")
	}
	completeDirectoryRead(t, h, request, read, false, false)
}
