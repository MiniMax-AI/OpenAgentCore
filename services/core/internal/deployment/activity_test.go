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
				name       string
				age        time.Duration
				busy, wake bool
				timeout    time.Duration
				want       bool
			}{
				{"recent", timeout - time.Second, false, false, timeout, false},
				{"boundary", timeout, false, false, timeout, true},
				{"elapsed", timeout + time.Second, false, false, timeout, true},
				{"busy", timeout + time.Second, true, false, timeout, false},
				{"wake", timeout + time.Second, false, true, timeout, false},
				{"missing_policy", timeout + time.Second, false, false, 0, false},
				{"invalid_policy", timeout + time.Second, false, false, -time.Second, false},
			} {
				t.Run(test.name, func(t *testing.T) {
					activity := Activity{ObservedAt: observed, LastActivity: observed.Add(-test.age), Busy: test.busy, WakeRequested: test.wake}
					if got := activity.ReadyToSuspend(test.timeout); got != test.want {
						t.Fatalf("suspension admission = %v, want %v with database clock skew %s", got, test.want, skew)
					}
				})
			}
		})
	}
}
