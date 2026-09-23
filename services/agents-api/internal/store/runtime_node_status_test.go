package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRuntimeNodeStatusUsesAuthenticatedFreshPresence(t *testing.T) {
	s, _, d := managerFixture(t, 1, 4)
	status := func(connected, ready bool) {
		t.Helper()
		got, err := s.RuntimeNodeStatus(t.Context(), d.LocalNodeID, "local-node-credential")
		if err != nil || got.NodeID != d.LocalNodeID || got.Connected != connected || got.ProviderReady != ready {
			t.Fatal(got, err)
		}
		raw, err := json.Marshal(got)
		if err != nil || strings.Contains(string(raw), "credential") || strings.Contains(string(raw), "backend_fingerprint") {
			t.Fatal("private identity leaked", string(raw), err)
		}
	}
	status(true, true)
	if _, err := s.RuntimeNodeStatus(t.Context(), d.LocalNodeID, "different-credential"); !errors.Is(err, ErrRuntimeNodeCredential) {
		t.Fatal("status admitted another credential", err)
	}
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_nodes SET provider_ready=false WHERE id=$1", d.LocalNodeID); err != nil {
		t.Fatal(err)
	}
	status(true, false)
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_nodes SET provider_ready=true,last_seen_at=clock_timestamp()-interval '46 seconds' WHERE id=$1", d.LocalNodeID); err != nil {
		t.Fatal(err)
	}
	status(false, false)
	onlineManagerNode(t, s, d.LocalNodeID)
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_deployment SET owner_epoch=owner_epoch+1 WHERE singleton=true"); err != nil {
		t.Fatal(err)
	}
	status(false, false)
}
