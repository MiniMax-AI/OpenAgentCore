package dispatch_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/agent"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func TestBoundRuntimeSwitchesSessionOnlyWhenOtherSessionIsIdle(t *testing.T) {
	var made []*controlledPreparation
	sender := &recSender{}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (preparedFixture, error) {
		p := &controlledPreparation{closed: make(chan struct{})}
		made = append(made, p)
		return p, nil
	})
	root := os.Getenv("OAC_RUNTIME_HOME")
	first, next := preparationRequest(), preparationRequest()
	next.SessionID = uuid.NewString()
	next.Configuration.AgentStateKey = next.SessionID
	ready := executorAdmission(t, r, sender, "first", first)
	var dirs [2]string
	for i, id := range []string{first.SessionID, next.SessionID} {
		dir, err := agent.StateDir(root, "codex", id)
		if err != nil || os.MkdirAll(dir, 0o700) != nil {
			t.Fatal(err)
		}
		dirs[i] = dir
	}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "busy", next))
	if got := waitPreparationStatus(t, sender, "busy", "rejected", ""); got.ErrorCode != "resource_unavailable" || len(made) != 1 {
		t.Fatalf("admitted Session was displaced: %+v", got)
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionRelease, "first", proto.ExecutionReleasePayload{Handle: ready.Handle})); err != nil {
		t.Fatal(err)
	}
	waitPreparationStatus(t, sender, "first", "released", "")
	executorAdmission(t, r, sender, "switch", next)
	waitPreparationClosed(t, made[0])
	if _, err := os.Stat(dirs[0]); !os.IsNotExist(err) {
		t.Fatal("old Session state kept")
	}
	if _, err := os.Stat(dirs[1]); err != nil {
		t.Fatal("new Session state removed")
	}
}
