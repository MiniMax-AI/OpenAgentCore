package deployment

import (
	"testing"
	"time"
)

func TestRuntimeIdleAdmissionUsesDatabaseClock(t *testing.T) {
	coreNow := time.Now()
	const timeout = time.Minute
	for _, skew := range []time.Duration{-269 * time.Second, 269 * time.Second} {
		t.Run(skew.String(), func(t *testing.T) {
			observed := coreNow.Add(skew)
			for _, test := range []struct {
				name                  string
				age                   time.Duration
				busy, wake, completed bool
				timeout               time.Duration
				want                  bool
			}{
				{"recent", timeout - time.Second, false, false, true, timeout, false},
				{"boundary", timeout, false, false, true, timeout, true},
				{"elapsed", timeout + time.Second, false, false, true, timeout, true},
				{"busy", timeout + time.Second, true, false, true, timeout, false},
				{"wake", timeout + time.Second, false, true, true, timeout, false},
				{"never_completed", timeout + time.Second, false, false, false, timeout, false},
				{"missing_policy", timeout + time.Second, false, false, true, 0, false},
				{"invalid_policy", timeout + time.Second, false, false, true, -time.Second, false},
			} {
				t.Run(test.name, func(t *testing.T) {
					activity := Activity{ObservedAt: observed, LastActivity: observed.Add(-test.age), Busy: test.busy, WakeRequested: test.wake, HasCompletedTurn: test.completed}
					if got := activity.ReadyToSuspend(test.timeout); got != test.want {
						t.Fatalf("suspension admission = %v, want %v with database clock skew %s", got, test.want, skew)
					}
				})
			}
		})
	}
}
