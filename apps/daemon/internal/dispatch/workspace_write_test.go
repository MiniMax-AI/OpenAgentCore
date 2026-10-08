package dispatch_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func localWriterRouter(t *testing.T) (*dispatch.Router, *recSender, proto.WorkspaceWritePayload, *testOwner) {
	t.Helper()
	environment, session := uuid.NewString(), preparationSessionID
	owner := newTestOwner(environment, session)
	sender := &recSender{}
	r, err := dispatch.New(dispatch.Config{Registry: agent.NewRegistry(), Sender: sender, Environments: owner.Resolve})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.Shutdown(ctx)
	})
	assign(t, r, session, environment)
	digest := sha256.Sum256([]byte("abc"))
	return r, sender, proto.WorkspaceWritePayload{Step: "begin", EnvironmentID: environment, SessionID: session, Path: "file", SizeBytes: 3, SHA256: hex.EncodeToString(digest[:])}, owner
}

func waitWorkspaceWrite(t *testing.T, sender *recSender, id, outcome string) proto.WorkspaceWriteResultPayload {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, env := range sender.snapshot() {
			if env.ID != id || env.Type != proto.TypeWorkspaceWriteResult {
				continue
			}
			var result proto.WorkspaceWriteResultPayload
			if env.DecodePayload(&result) != nil {
				t.Fatal("bad write result")
			}
			if result.Outcome == outcome {
				return result
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("write result missing", id, outcome)
	return proto.WorkspaceWriteResultPayload{}
}

func TestLocalUploadRequiresExactScopeAndCompleteBody(t *testing.T) {
	r, sender, request, owner := localWriterRouter(t)
	for _, field := range []string{"environment", "session"} {
		bad := request
		if field == "environment" {
			bad.EnvironmentID = uuid.NewString()
		} else {
			bad.SessionID = uuid.NewString()
		}
		id := uuid.NewString()
		if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, bad)); err != nil {
			t.Fatal(err)
		}
		waitWorkspaceWrite(t, sender, id, "rejected")
	}
	id := uuid.NewString()
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, request)); err != nil {
		t.Fatal(err)
	}
	waitWorkspaceWrite(t, sender, id, "ready")
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, request)); err == nil {
		t.Fatal("duplicate transfer accepted")
	}
	for offset, part := range []string{"a", "bc"} {
		if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, proto.WorkspaceWritePayload{Step: "chunk", Offset: offset, Data: []byte(part)})); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := owner.file("file"); ok {
		t.Fatal("file created before commit")
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, proto.WorkspaceWritePayload{Step: "commit"})); err != nil {
		t.Fatal(err)
	}
	if got := waitWorkspaceWrite(t, sender, id, "completed"); got.SizeBytes != 3 {
		t.Fatal(got)
	}
	if data, _ := owner.file("file"); string(data) != "abc" {
		t.Fatal("committed bytes differ", data)
	}
}

func TestLocalUploadRejectsReorderedOrCorruptBodiesWithoutMutation(t *testing.T) {
	for _, mode := range []string{"offset", "digest", "short"} {
		t.Run(mode, func(t *testing.T) {
			r, sender, request, owner := localWriterRouter(t)
			id := uuid.NewString()
			_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, request))
			chunk := proto.WorkspaceWritePayload{Step: "chunk", Data: []byte("abc")}
			switch mode {
			case "offset":
				chunk.Offset = 1
			case "digest":
				chunk.Data = []byte("bad")
			case "short":
				chunk.Data = []byte("a")
			}
			_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, chunk))
			_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, proto.WorkspaceWritePayload{Step: "commit"}))
			waitWorkspaceWrite(t, sender, id, "rejected")
			if _, ok := owner.file("file"); ok {
				t.Fatal("bad transfer mutated workspace")
			}
		})
	}
}

func TestLocalUploadReportsDestinationConflictsAndReleasesOwner(t *testing.T) {
	for helperError, reason := range map[string]string{
		"destination_directory": proto.WorkspaceWriteReasonDirectory,
		"unsafe_destination":    proto.WorkspaceWriteReasonUnsafe,
		"write_failed":          "",
	} {
		t.Run(helperError, func(t *testing.T) {
			r, sender, request, owner := localWriterRouter(t)
			switch helperError {
			case "destination_directory":
				owner.put("file", nil)
			case "unsafe_destination":
				owner.put("file", []byte("existing"))
			case "write_failed":
				owner.put("parent", []byte{})
				request.Path = "parent/file"
			}
			id := uuid.NewString()
			for _, p := range []proto.WorkspaceWritePayload{request, {Step: "chunk", Data: []byte("abc")}, {Step: "commit"}} {
				if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, p)); err != nil {
					t.Fatal(err)
				}
			}
			if got := waitWorkspaceWrite(t, sender, id, "rejected"); got.ErrorCode != "write_rejected" || got.Reason != reason {
				t.Fatal(got)
			}
			next := uuid.NewString()
			if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, next, request)); err != nil {
				t.Fatal(err)
			}
			waitWorkspaceWrite(t, sender, next, "ready")
		})
	}
}

func TestReleaseFencesUnfinishedWorkspaceWrite(t *testing.T) {
	r, sender, request, owner := localWriterRouter(t)
	id := uuid.NewString()
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, request)); err != nil {
		t.Fatal(err)
	}
	waitWorkspaceWrite(t, sender, id, "ready")
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, proto.WorkspaceWritePayload{Step: "chunk", Data: []byte("abc")})); err != nil {
		t.Fatal(err)
	}
	waitWorkspaceWrite(t, sender, id, "received")
	release(t, r, preparationSessionID, "release", 2, false)
	if got := waitWorkspaceWrite(t, sender, id, "rejected"); got.ErrorCode != proto.AssignmentStale {
		t.Fatal("release did not fence the write", got)
	}
	if got := waitAssignmentStatus(t, sender, "release"); got.State != proto.AssignmentReleased {
		t.Fatal(got)
	}
	// The release replies only after the write's result.
	for _, frame := range sender.snapshot() {
		if frame.Type == proto.TypeAssignmentStatus && frame.ID == "release" {
			t.Fatal("release replied before the write settled")
		}
		if frame.Type == proto.TypeWorkspaceWriteResult && frame.ID == id && frame.Assignment == ref(preparationSessionID) {
			var result proto.WorkspaceWriteResultPayload
			if frame.DecodePayload(&result) == nil && result.Outcome == "rejected" {
				break
			}
		}
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, proto.WorkspaceWritePayload{Step: "commit"})); err != nil {
		t.Fatal(err)
	}
	if _, ok := owner.file("file"); ok {
		t.Fatal("a released assignment's write applied")
	}
}

func TestReleaseWaitsUntilTheWriteResultIsSent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, sender, request, owner := localWriterRouter(t)
		sent := make(chan struct{})
		sender.hold = func(env proto.Envelope) {
			var result proto.WorkspaceWriteResultPayload
			if env.Type == proto.TypeWorkspaceWriteResult && env.DecodePayload(&result) == nil && result.Outcome == "completed" {
				<-sent
			}
		}
		id := uuid.NewString()
		for _, step := range []proto.WorkspaceWritePayload{request, {Step: "chunk", Data: []byte("abc")}, {Step: "commit"}} {
			if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, step)); err != nil {
				t.Fatal(err)
			}
		}
		// The write has applied and released its owner; its result is not sent yet.
		synctest.Wait()
		release(t, r, preparationSessionID, "release", 2, false)
		synctest.Wait()
		if hasFrame(sender, proto.TypeAssignmentStatus, "release") {
			t.Error("the release replied before the write's result was sent")
		}
		close(sent)
		if got := waitAssignmentStatus(t, sender, "release"); got.State != proto.AssignmentReleased {
			t.Fatal(got)
		}
		waitWorkspaceWrite(t, sender, id, "completed")
		if data, _ := owner.file("file"); string(data) != "abc" {
			t.Fatal("the committed write did not apply", data)
		}
	})
}
