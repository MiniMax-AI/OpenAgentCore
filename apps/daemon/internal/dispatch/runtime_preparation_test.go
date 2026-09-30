package dispatch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentskill"
	"github.com/google/uuid"
)

type capabilitiesTestSender struct{ frames chan proto.Envelope }

func (s *capabilitiesTestSender) Send(ctx context.Context, env proto.Envelope) error {
	select {
	case s.frames <- env:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func capabilitiesTestRouter(t *testing.T) (*Router, *capabilitiesTestSender, string, string) {
	t.Helper()
	environment, session := uuid.NewString(), uuid.NewString()
	binding, err := localworkspace.New(environment, session, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sender := &capabilitiesTestSender{frames: make(chan proto.Envelope, 64)}
	router, err := New(Config{Registry: agent.NewRegistry(), Sender: sender, LocalWorkspace: binding})
	if err != nil {
		t.Fatal(err)
	}
	return router, sender, environment, session
}

func capabilityEnvelope(t *testing.T, id string, request proto.RuntimePreparePayload) proto.Envelope {
	t.Helper()
	env, err := proto.NewEnvelope(proto.TypeRuntimePrepare, id, request)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func capabilityBegin(environment, session string, body []byte) proto.RuntimePreparePayload {
	digest := sha256.Sum256(body)
	return proto.RuntimePreparePayload{
		Step: "begin", Action: "skill", EnvironmentID: environment, SessionID: session,
		Skill:     &agentskill.Metadata{Type: "inline", Name: "proof", Description: "A proof."},
		SizeBytes: len(body), SHA256: hex.EncodeToString(digest[:]),
	}
}

func capabilitiesReceipt(t *testing.T, sender *capabilitiesTestSender, id, outcome string) proto.RuntimePrepareResultPayload {
	t.Helper()
	select {
	case env := <-sender.frames:
		var result proto.RuntimePrepareResultPayload
		if env.Type != proto.TypeRuntimePrepareResult || env.ID != id || env.DecodePayload(&result) != nil || result.Outcome != outcome {
			t.Fatalf("unexpected capability receipt: id=%s type=%s result=%+v", env.ID, env.Type, result)
		}
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("missing capability receipt")
	}
	return proto.RuntimePrepareResultPayload{}
}

func shutdownCapabilitiesRouter(t *testing.T, router *Router) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := router.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimePreparationTransferValidatesCompleteBodyBeforeMutation(t *testing.T) {
	for _, mode := range []string{"offset", "digest", "short", "oversized-chunk"} {
		t.Run(mode, func(t *testing.T) {
			r, sender, environment, session := capabilitiesTestRouter(t)
			defer shutdownCapabilitiesRouter(t, r)
			body := bytes.Repeat([]byte("a"), proto.RuntimePrepareChunkBytes+1)
			request := capabilityBegin(environment, session, body)
			if mode == "digest" {
				request.SHA256 = stringsOfZeroDigest()
			}
			id := uuid.NewString()
			if err := r.Handle(t.Context(), capabilityEnvelope(t, id, request)); err != nil {
				t.Fatal(err)
			}
			capabilitiesReceipt(t, sender, id, "ready")
			r.mu.Lock()
			owner := r.runtimePreparation
			r.mu.Unlock()
			duplicate := uuid.NewString()
			if err := r.Handle(t.Context(), capabilityEnvelope(t, duplicate, request)); err != nil {
				t.Fatal(err)
			}
			if got := capabilitiesReceipt(t, sender, duplicate, "rejected"); got.ErrorCode != "runtime_preparation_capacity" {
				t.Fatal(got)
			}
			switch mode {
			case "offset":
				_ = r.Handle(t.Context(), capabilityEnvelope(t, id, proto.RuntimePreparePayload{Step: "chunk", Offset: 1, Data: []byte("a")}))
			case "oversized-chunk":
				_ = r.Handle(t.Context(), capabilityEnvelope(t, id, proto.RuntimePreparePayload{Step: "chunk", Data: body}))
			default:
				if err := r.Handle(t.Context(), capabilityEnvelope(t, id, proto.RuntimePreparePayload{Step: "chunk", Data: body[:proto.RuntimePrepareChunkBytes]})); err != nil {
					t.Fatal(err)
				}
				if got := capabilitiesReceipt(t, sender, id, "received"); got.Offset != proto.RuntimePrepareChunkBytes {
					t.Fatal(got)
				}
				if mode == "digest" {
					_ = r.Handle(t.Context(), capabilityEnvelope(t, id, proto.RuntimePreparePayload{Step: "chunk", Offset: proto.RuntimePrepareChunkBytes, Data: body[proto.RuntimePrepareChunkBytes:]}))
					capabilitiesReceipt(t, sender, id, "received")
				}
				_ = r.Handle(t.Context(), capabilityEnvelope(t, id, proto.RuntimePreparePayload{Step: "commit"}))
			}
			capabilitiesReceipt(t, sender, id, "rejected")
			r.mu.Lock()
			defer r.mu.Unlock()
			if owner.apply || owner.data != nil || r.runtimePreparation != nil {
				t.Fatal("invalid body retained or admitted a mutation")
			}
		})
	}
}

func stringsOfZeroDigest() string { return hex.EncodeToString(make([]byte, sha256.Size)) }

func TestRuntimePreparationBeginRequiresExactBindingAndBounds(t *testing.T) {
	r, sender, environment, session := capabilitiesTestRouter(t)
	defer shutdownCapabilitiesRouter(t, r)
	for _, mode := range []string{"environment", "session", "size", "nil-binding"} {
		request := capabilityBegin(environment, session, []byte("abc"))
		switch mode {
		case "environment":
			request.EnvironmentID = uuid.NewString()
		case "session":
			request.SessionID = uuid.NewString()
		case "size":
			request.SizeBytes = proto.RuntimePrepareMaxBytes + 1
		case "nil-binding":
			r.localWorkspace = nil
		}
		id := uuid.NewString()
		if err := r.Handle(t.Context(), capabilityEnvelope(t, id, request)); err != nil {
			t.Fatal(err)
		}
		capabilitiesReceipt(t, sender, id, "rejected")
		if r.runtimePreparation != nil {
			t.Fatal("invalid scope allocated a transfer")
		}
	}
}

func TestRuntimePreparationPreparationExcludesOwnedResources(t *testing.T) {
	for _, mode := range []string{"write", "export", "read", "run", "idle", "executor", "preparation"} {
		t.Run(mode, func(t *testing.T) {
			r, sender, environment, session := capabilitiesTestRouter(t)
			switch mode {
			case "write":
				r.workspaceWrite = &workspaceUpload{}
			case "export":
				r.workspaceExport = &workspaceExport{}
			case "read":
				r.workspaceReads = map[string]struct{}{"read": {}}
			case "run":
				r.sessions["run"] = &sessionState{}
			case "idle":
				r.idle["state"] = map[*sessionState]struct{}{}
			case "executor":
				r.executors[session] = &executorState{}
			case "preparation":
				r.preparations["p"] = &preparationState{owns: true}
			}
			id := uuid.NewString()
			if err := r.Handle(t.Context(), capabilityEnvelope(t, id, capabilityBegin(environment, session, []byte("abc")))); err != nil {
				t.Fatal(err)
			}
			if got := capabilitiesReceipt(t, sender, id, "rejected"); got.ErrorCode != "resource_unavailable" {
				t.Fatal(got)
			}
			if r.runtimePreparation != nil {
				t.Fatal("busy Runtime admitted capability preparation")
			}
			r.workspaceWrite = nil
			r.workspaceExport = nil
			r.workspaceReads = nil
			clear(r.sessions)
			clear(r.idle)
			clear(r.executors)
			clear(r.preparations)
			shutdownCapabilitiesRouter(t, r)
		})
	}
}

func TestRuntimePreparationUploadBlocksWorkspaceWriteAndSuspension(t *testing.T) {
	r, sender, environment, session := capabilitiesTestRouter(t)
	id := uuid.NewString()
	if err := r.Handle(t.Context(), capabilityEnvelope(t, id, capabilityBegin(environment, session, []byte("abc")))); err != nil {
		t.Fatal(err)
	}
	capabilitiesReceipt(t, sender, id, "ready")
	if err := r.Quiesce(t.Context(), proto.EnvironmentSuspendPayload{EnvironmentID: environment, SuspendID: uuid.NewString()}); !errors.Is(err, ErrRouterBusy) {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("abc"))
	write, err := proto.NewEnvelope(proto.TypeWorkspaceWrite, uuid.NewString(), proto.WorkspaceWritePayload{Step: "begin", EnvironmentID: environment, SessionID: session, Path: "proof", SizeBytes: 3, SHA256: hex.EncodeToString(digest[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Handle(t.Context(), write); err != nil {
		t.Fatal(err)
	}
	select {
	case env := <-sender.frames:
		var result proto.WorkspaceWriteResultPayload
		if env.Type != proto.TypeWorkspaceWriteResult || env.DecodePayload(&result) != nil || result.Outcome != "rejected" || result.ErrorCode != "resource_unavailable" {
			t.Fatal("workspace mutation bypassed capability transfer")
		}
	case <-time.After(time.Second):
		t.Fatal("missing write rejection")
	}
	r.mu.Lock()
	owner := r.runtimePreparation
	r.mu.Unlock()
	shutdownCapabilitiesRouter(t, r)
	if owner.data != nil || r.runtimePreparation != nil {
		t.Fatal("disconnect retained uncommitted body")
	}
}

func TestRuntimePreparationCancellationKeepsOwnershipUntilApplyStops(t *testing.T) {
	r, sender, environment, session := capabilitiesTestRouter(t)
	ctx, cancel := context.WithCancel(context.Background())
	id := uuid.NewString()
	request := proto.RuntimePreparePayload{Step: "begin", Action: "finalize", EnvironmentID: environment, SessionID: session, Sources: &agentcapabilities.Input{}}
	owner := &runtimePreparationTransfer{envelope: capabilityEnvelope(t, id, request), request: request, ready: make(chan struct{}), cancel: cancel, finished: true, apply: true}
	close(owner.ready)
	r.runtimePreparation = owner
	r.shutdownWG.Add(1)
	started, interrupted, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	retained := filepath.Join(t.TempDir(), "installed.json")
	go r.runRuntimePreparationTransfer(ctx, owner, func(ctx context.Context, got proto.RuntimePreparePayload, data []byte) error {
		if got.Action != "finalize" || len(data) != 0 {
			return agentcapabilities.ErrInvalid
		}
		close(started)
		<-ctx.Done()
		close(interrupted)
		<-release
		return os.WriteFile(retained, []byte("retained"), 0400)
	})
	<-started
	wait, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err := r.Shutdown(wait)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown discarded running mutation: %v", err)
	}
	<-interrupted
	r.mu.Lock()
	owned := r.runtimePreparation == owner
	r.mu.Unlock()
	if !owned {
		t.Fatal("cancel released unsettled capability ownership")
	}
	close(release)
	shutdownCapabilitiesRouter(t, r)
	capabilitiesReceipt(t, sender, id, "completed")
	if _, err := os.Stat(retained); err != nil {
		t.Fatal("shutdown deleted installation result")
	}
	if r.runtimePreparation != nil {
		t.Fatal("confirmed completion retained capacity")
	}
}

func TestRuntimePreparationInitializationReceipts(t *testing.T) {
	for _, code := range []int{-1, 0, 1, 255, 256} {
		got := runtimePreparationResult(&localworkspace.InitializationFailure{ExitCode: &code}, 0)
		if code > 0 && code <= 255 {
			if got.Outcome != "failed" || got.ExitCode != code {
				t.Fatalf("lost confirmed exit code: %+v", got)
			}
		} else if got.Outcome != "unknown" {
			t.Fatalf("accepted invalid failure receipt: %+v", got)
		}
	}
	if got := runtimePreparationResult(&localworkspace.InitializationFailure{}, 0); got.Outcome != "failed" || got.ExitCode != 0 {
		t.Fatalf("lost confirmed generic failure: %+v", got)
	}
	if got := runtimePreparationResult(errors.Join(&localworkspace.InitializationFailure{}, context.Canceled), 0); got.Outcome != "unknown" {
		t.Fatalf("cancellation reported confirmed: %+v", got)
	}
}

func TestRuntimePreparationResultCategoriesAndUnknownOwnership(t *testing.T) {
	for _, tc := range []struct {
		err           error
		outcome, code string
	}{
		{nil, "completed", ""},
		{agentcapabilities.ErrInvalid, "failed", "runtime_preparation_failed"},
		{context.DeadlineExceeded, "unknown", "runtime_preparation_unconfirmed"},
		{errors.Join(agentcapabilities.ErrInvalid, context.Canceled), "unknown", "runtime_preparation_unconfirmed"},
		{errors.New("private native diagnostic"), "unknown", "runtime_preparation_unconfirmed"},
	} {
		got := runtimePreparationResult(tc.err, 3)
		if got.Outcome != tc.outcome || got.ErrorCode != tc.code || !proto.ValidRuntimePrepareResult(got, "completed", 0, 3) {
			t.Fatalf("unsafe result: %+v", got)
		}
	}
	r, sender, environment, session := capabilitiesTestRouter(t)
	ctx, cancel := context.WithCancel(context.Background())
	id := uuid.NewString()
	request := capabilityBegin(environment, session, []byte("abc"))
	owner := &runtimePreparationTransfer{envelope: capabilityEnvelope(t, id, request), request: request, data: []byte("abc"), ready: make(chan struct{}), cancel: cancel, finished: true, apply: true}
	close(owner.ready)
	r.runtimePreparation = owner
	r.shutdownWG.Add(1)
	go r.runRuntimePreparationTransfer(ctx, owner, func(context.Context, proto.RuntimePreparePayload, []byte) error { return context.DeadlineExceeded })
	capabilitiesReceipt(t, sender, id, "unknown")
	r.mu.Lock()
	owned := r.runtimePreparation == owner && owner.uncertain && owner.data == nil
	r.mu.Unlock()
	if !owned {
		t.Fatal("unknown mutation released its ownership")
	}
	wait, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := r.Shutdown(wait); err == nil {
		t.Fatal("shutdown claimed uncertain mutation settled")
	}
}
