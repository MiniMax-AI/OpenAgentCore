package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func TestNativeDirectoryFailureMapping(t *testing.T) {
	for _, test := range []struct {
		err           error
		outcome, code string
	}{
		{ErrWorkspaceNotDirectory, "rejected", proto.WorkspaceReadNotDirectory},
		{ErrWorkspaceReadInvalid, "rejected", "invalid_request"},
		{fs.ErrNotExist, "rejected", "not_found"},
		{fs.ErrPermission, "rejected", "permission_denied"},
		{ErrWorkspaceReadUncertain, "unknown", "read_unconfirmed"},
	} {
		got := workspaceReadFailure(test.err)
		if got.Outcome != test.outcome || got.ErrorCode != test.code {
			t.Fatal(test.err, got)
		}
	}
}

// quarantinedEnvironment is an owner that cannot observe a write's outcome.
type quarantinedEnvironment struct{ stubEnvironment }

func (quarantinedEnvironment) WriteWorkspaceFile(context.Context, string, []byte) (WorkspaceWriteResult, error) {
	return WorkspaceWriteResult{}, ErrWorkspaceWriteUncertain
}

// An unknown write leaves its uncertainty to the Environment owner: the
// Session's next write reaches the quarantined owner, and Shutdown settles.
func TestUnknownWriteLeavesUncertaintyToTheOwner(t *testing.T) {
	r, sender, environment, session := capabilitiesTestRouter(t)
	r.assignments[session].environment = quarantinedEnvironment{}
	digest := sha256.Sum256([]byte("abc"))
	begin := proto.WorkspaceWritePayload{Step: "begin", EnvironmentID: environment, SessionID: session, Path: "file", SizeBytes: 3, SHA256: hex.EncodeToString(digest[:])}
	for range 2 {
		id := uuid.NewString()
		var result proto.WorkspaceWriteResultPayload
		for _, step := range []proto.WorkspaceWritePayload{begin, {Step: "chunk", Data: []byte("abc")}, {Step: "commit"}} {
			env, err := proto.NewEnvelope(proto.TypeWorkspaceWrite, id, step)
			if err != nil {
				t.Fatal(err)
			}
			env.Assignment = capabilityRef
			if err := r.Handle(t.Context(), env); err != nil {
				t.Fatal(err)
			}
			if err := (<-sender.frames).DecodePayload(&result); err != nil {
				t.Fatal(err)
			}
		}
		if result.Outcome != "unknown" || result.ErrorCode != "write_unconfirmed" {
			t.Fatalf("write = %+v, want unknown", result)
		}
	}
	shutdownCapabilitiesRouter(t, r)
}
