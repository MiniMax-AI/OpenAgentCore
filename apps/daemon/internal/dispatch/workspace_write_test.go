package dispatch_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/dispatch"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/localworkspace"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func localWriterRouter(t *testing.T) (*dispatch.Router, *recSender, proto.WorkspaceWritePayload, string) {
	t.Helper()
	workspace := t.TempDir()
	environment, session := uuid.NewString(), uuid.NewString()
	binding, err := localworkspace.New(environment, session, workspace)
	if err != nil {
		t.Fatal(err)
	}
	sender := &recSender{}
	r, err := dispatch.New(dispatch.Config{Registry: agent.NewRegistry(), Sender: sender, LocalWorkspace: binding})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.Shutdown(ctx)
	})
	digest := sha256.Sum256([]byte("abc"))
	return r, sender, proto.WorkspaceWritePayload{Step: "begin", EnvironmentID: environment, SessionID: session, Path: "file", SizeBytes: 3, SHA256: hex.EncodeToString(digest[:])}, workspace
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
	r, sender, request, workspace := localWriterRouter(t)
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
	if _, err := os.Stat(filepath.Join(workspace, "file")); !os.IsNotExist(err) {
		t.Fatal("file created before commit")
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, proto.WorkspaceWritePayload{Step: "commit"})); err != nil {
		t.Fatal(err)
	}
	if got := waitWorkspaceWrite(t, sender, id, "completed"); got.SizeBytes != 3 {
		t.Fatal(got)
	}
	if data, err := os.ReadFile(filepath.Join(workspace, "file")); err != nil || string(data) != "abc" {
		t.Fatal("committed bytes differ", err)
	}
}

func TestLocalUploadRejectsReorderedOrCorruptBodiesWithoutMutation(t *testing.T) {
	for _, mode := range []string{"offset", "digest", "short"} {
		t.Run(mode, func(t *testing.T) {
			r, sender, request, workspace := localWriterRouter(t)
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
			if _, err := os.Stat(filepath.Join(workspace, "file")); !os.IsNotExist(err) {
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
			r, sender, request, workspace := localWriterRouter(t)
			switch helperError {
			case "destination_directory":
				if err := os.Mkdir(filepath.Join(workspace, "file"), 0700); err != nil {
					t.Fatal(err)
				}
			case "unsafe_destination":
				if err := os.WriteFile(filepath.Join(workspace, "file"), []byte("existing"), 0600); err != nil {
					t.Fatal(err)
				}
			case "write_failed":
				if err := os.WriteFile(filepath.Join(workspace, "parent"), nil, 0600); err != nil {
					t.Fatal(err)
				}
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
