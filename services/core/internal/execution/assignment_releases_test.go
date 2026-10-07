package execution

import (
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func pendingRelease(session string) sessions.AssignmentRelease {
	return sessions.AssignmentRelease{Assignment: proto.AssignmentRef{SessionID: session, AssignmentID: session, Epoch: 2}}
}

func TestFailedReleaseBacksOff(t *testing.T) {
	failing := pendingRelease("failing")
	retries, now := releaseRetries{}, time.Unix(0, 0)
	due := func(at time.Time) bool { return len(retries.due([]sessions.AssignmentRelease{failing}, at)) == 1 }
	retries.record(failing.Assignment, false, now)
	if due(now) || !due(now.Add(time.Second)) {
		t.Fatal("a failed release did not wait a second")
	}
	for range 8 {
		retries.record(failing.Assignment, false, now)
	}
	if due(now.Add(59*time.Second)) || !due(now.Add(time.Minute)) {
		t.Fatalf("delay = %s, want a minute", retries[failing.Assignment].delay)
	}
	retries.record(failing.Assignment, true, now)
	retries.record(pendingRelease("settled").Assignment, false, now)
	retries.keep(nil)
	if len(retries) != 0 {
		t.Fatal("settled or acknowledged releases kept a retry", retries)
	}
}

// With one slot, two releases whose Runtimes never acknowledge alternate
// while each takes the full acknowledgement timeout; a third still runs.
func TestFailingReleasesCannotStarveAnother(t *testing.T) {
	releases := []sessions.AssignmentRelease{pendingRelease("a"), pendingRelease("b"), pendingRelease("c")}
	retries, now := releaseRetries{}, time.Unix(0, 0)
	for range 3 {
		next := retries.due(releases, now)[0].Assignment
		if next.SessionID == "c" {
			return
		}
		now = now.Add(2 * time.Minute)
		retries.record(next, false, now)
	}
	t.Fatal("releases that keep failing starved a release never attempted")
}
