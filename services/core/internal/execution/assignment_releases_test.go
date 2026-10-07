package execution

import (
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestFailedReleaseBacksOffAndLeavesLaterReleasesDue(t *testing.T) {
	failing := proto.AssignmentRef{SessionID: "failing", AssignmentID: "failing", Epoch: 2}
	later := proto.AssignmentRef{SessionID: "later", AssignmentID: "later", Epoch: 2}
	retries, now := releaseRetries{}, time.Unix(0, 0)
	retries.record(failing, false, now)
	if retries.due(failing, now) || !retries.due(failing, now.Add(time.Second)) || !retries.due(later, now) {
		t.Fatal("a failed release did not wait a second while later releases stayed due")
	}
	for range 8 {
		retries.record(failing, false, now)
	}
	if retries.due(failing, now.Add(59*time.Second)) || !retries.due(failing, now.Add(time.Minute)) {
		t.Fatalf("delay = %s, want a minute", retries[failing].delay)
	}
	retries.keep([]sessions.AssignmentRelease{{Assignment: later}})
	retries.record(later, true, now)
	if len(retries) != 0 {
		t.Fatal("settled or acknowledged releases kept a retry", retries)
	}
}
