package store

import (
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/google/uuid"
)

func TestRuntimeNodeObservationRetainsResourcesAndFencesStaleResults(t *testing.T) {
	s, w, d := managerFixture(t, 1, 4)
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, managerSessionInput("observation", d.LocalNodeID))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := w.ReserveRuntimeAllocation(t.Context(), tenant, session.Environment.ID, d.InstallationID, device.HashCredential("runtime"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err = w.ObserveRuntimeRunning(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.RecordRuntimeObservation(t.Context(), owner, "node_unavailable"); err != nil {
		t.Fatal(err)
	}
	retained, err := s.GetRuntimeAllocation(t.Context(), tenant, session.Environment.ID)
	if err != nil || retained.State != "running" || retained.ID != owner.ID || retained.ObservationError != "node_unavailable" {
		t.Fatal(retained, err)
	}
	if err := s.RemoveRuntimeNode(t.Context(), d.LocalNodeID); !errors.Is(err, ErrRuntimeNodeInUse) {
		t.Fatal("diagnostic released resource", err)
	}
	// A new lifecycle observation must not be erased by an earlier result.
	if _, err := s.pool.Exec(t.Context(), "UPDATE runtime_allocations SET compute_revision=compute_revision+1,observation_error='resource_missing' WHERE id=$1", owner.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.RecordRuntimeObservation(t.Context(), owner, ""); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetRuntimeAllocation(t.Context(), tenant, session.Environment.ID)
	if err != nil || current.ObservationError != "resource_missing" {
		t.Fatal("stale success erased newer failure", current, err)
	}
	if err := w.RecordRuntimeObservation(t.Context(), current, ""); err != nil {
		t.Fatal(err)
	}
	if err := w.RecordRuntimeObservation(t.Context(), owner, "node_unavailable"); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.GetRuntimeAllocation(t.Context(), tenant, session.Environment.ID)
	if err != nil || recovered.ObservationError != "" || recovered.ID != owner.ID {
		t.Fatal("stale error replaced recovered observation", recovered, err)
	}
	if err := w.RecordRuntimeObservation(t.Context(), current, "secret provider exception"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("raw diagnostics accepted", err)
	}
}
func TestRuntimeNodeStaleEpochCannotReplaceCurrentConnection(t *testing.T) {
	s, w, d := managerFixture(t, 1, 4)
	epoch := managerEpoch(t, s)
	deploymentConfigure(t, w, &d)
	connection := onlineManagerNode(t, s, d.LocalNodeID)
	if err := s.ConnectRuntimeNode(t.Context(), d.LocalNodeID, uuid.NewString(), epoch); !errors.Is(err, ErrRuntimeNodeCredential) {
		t.Fatal("old Core replaced new connection", err)
	}
	if err := s.DisconnectRuntimeNode(t.Context(), d.LocalNodeID, connection, epoch); err != nil {
		t.Fatal(err)
	}
	if err := s.HeartbeatRuntimeNode(t.Context(), d.LocalNodeID, connection, epoch, RuntimeNodeHealth{ProviderReady: false}); !errors.Is(err, ErrRuntimeNodeCredential) {
		t.Fatal("old Core rewrote health", err)
	}
	nodes, err := s.ListRuntimeNodes(t.Context())
	if err != nil || !nodes[0].Online || !nodes[0].ProviderReady {
		t.Fatal("stale callback changed current epoch", nodes, err)
	}
}
