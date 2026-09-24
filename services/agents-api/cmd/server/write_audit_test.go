package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWriteAuditRetention(t *testing.T) {
	for _, test := range []struct {
		value string
		want  time.Duration
		bad   bool
	}{{"", 90 * 24 * time.Hour, false}, {"24h", 24 * time.Hour, false}, {"0", 0, true}, {"30m", 0, true}, {"-1h", 0, true}, {"90d", 0, true}} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv("AGENTS_API_WRITE_AUDIT_RETENTION", test.value)
			got, err := writeAuditRetention()
			if (err != nil) != test.bad || (!test.bad && got != test.want) {
				t.Fatalf("%v %v", got, err)
			}
		})
	}
}

type auditPruneProbe struct {
	cancel   context.CancelFunc
	called   bool
	cutoff   time.Time
	limit    int
	deadline bool
}

func (p *auditPruneProbe) DeleteExpiredWriteOperations(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	p.called = true
	p.cutoff = cutoff
	p.limit = limit
	_, p.deadline = ctx.Deadline()
	p.cancel()
	return 0, errors.New("test")
}
func TestWriteAuditCleanupBoundedAndCancellable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &auditPruneProbe{cancel: cancel}
	before := time.Now().Add(-24 * time.Hour)
	runWriteAuditCleanup(ctx, probe, 24*time.Hour)
	if !probe.called || probe.limit != 1000 || !probe.deadline || probe.cutoff.Before(before) || probe.cutoff.After(time.Now().Add(-24*time.Hour)) {
		t.Fatalf("bad cleanup %+v", probe)
	}
}
