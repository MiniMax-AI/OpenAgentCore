package store

import (
	"testing"
	"time"
)

// Retention uses the Runtime history cleanup operation and never deletes nodes.
func TestNodeHostHistoryRetentionKeepsNode(t *testing.T) {
	s, _, d := managerFixture(t, 2, 8)
	now := time.Now().UTC()
	for _, at := range []time.Time{now.Add(-8 * 24 * time.Hour), now.Add(-2 * time.Minute)} {
		if _, err := s.pool.Exec(t.Context(), "INSERT INTO node_host_history_samples(node_id, observed_at) VALUES($1,$2)", d.LocalNodeID, at); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.PruneRuntimeHistorySamples(t.Context(), now.Add(-7*24*time.Hour).UnixNano()); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := deploymentService(t, s).NodeDetail(t.Context(), d.LocalNodeID, "1h"); err != nil {
		t.Fatal(err)
	}
}
