package dispatch

import (
	"errors"
	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
	"io/fs"
	"testing"
)

func TestNativeDirectoryFailureMapping(t *testing.T) {
	for _, test := range []struct {
		err           error
		outcome, code string
	}{
		{agent.ErrWorkspaceNotDirectory, "rejected", proto.WorkspaceReadNotDirectory},
		{agent.ErrWorkspaceReadInvalid, "rejected", "invalid_request"},
		{fs.ErrNotExist, "rejected", "not_found"},
		{fs.ErrPermission, "rejected", "permission_denied"},
		{agent.ErrWorkspaceReadUncertain, "unknown", "read_unconfirmed"},
	} {
		got := workspaceReadResult(agent.WorkspaceReadResult{}, test.err, 0)
		if got.Outcome != test.outcome || got.ErrorCode != test.code {
			t.Fatal(test.err, got)
		}
	}
}
func TestLocalUploadUnknownRetainsOwner(t *testing.T) {
	sender := exportSender{make(chan proto.Envelope, 8)}
	r, err := New(Config{Registry: agent.NewRegistry(), Sender: sender})
	if err != nil {
		t.Fatal(err)
	}
	got := workspaceWriteResult(agent.WorkspaceWriteResult{}, errors.New("unconfirmed mutation"), 3)
	if got.Outcome != "unknown" {
		t.Fatal(got)
	}
	r.workspaceWrite = &workspaceUpload{envelope: proto.Envelope{ID: uuid.NewString()}, finished: true, uncertain: true}
	request := proto.WorkspaceWritePayload{Step: "begin", EnvironmentID: uuid.NewString(), SessionID: uuid.NewString(), Path: "file", SizeBytes: 0, SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}
	env, _ := proto.NewEnvelope(proto.TypeWorkspaceWrite, uuid.NewString(), request)
	if err = r.Handle(t.Context(), env); err != nil {
		t.Fatal(err)
	}
	reply := <-sender.replies
	var result proto.WorkspaceWriteResultPayload
	if err = reply.DecodePayload(&result); err != nil || result.Outcome != "rejected" || result.ErrorCode != "write_capacity" {
		t.Fatal(result, err)
	}
	if err = r.Shutdown(t.Context()); err == nil {
		t.Fatal("shutdown declared uncertain mutation settled")
	}
}
