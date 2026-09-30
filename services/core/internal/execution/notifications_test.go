package execution

import (
	"sync"
	"testing"
)

func TestExecutionNotificationsKeepCommitHintsAndIsolateSessions(t *testing.T) {
	notifications := &executionNotifications{}
	one, leaveOne := notifications.subscribe("tenant", "one")
	other, leaveOther := notifications.subscribe("other-tenant", "one")
	defer leaveOther()
	var writers sync.WaitGroup
	for range 20 {
		writers.Go(func() { notifications.notify("tenant", "one") })
	}
	writers.Wait()
	select {
	case <-one:
	default:
		t.Fatal("committed change was lost")
	}
	select {
	case <-other:
		t.Fatal("hint crossed tenant ownership")
	default:
	}
	select {
	case <-one:
		t.Fatal("hints did not coalesce")
	default:
	}
	notifications.notify("tenant", "one")
	select {
	case <-one:
	default:
		t.Fatal("next commit was lost")
	}
	leaveOne()
	notifications.notify("tenant", "one")
	select {
	case <-one:
		t.Fatal("detached observer received a hint")
	default:
	}
	leaveOther()
	if len(notifications.waiters) != 0 {
		t.Fatal("subscriptions leaked")
	}
}
