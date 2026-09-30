package store

import (
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestEnvironmentConnectionRecoveryFencesLostOwnerAcrossPages(t *testing.T) {
	s, pool := testStore(t)
	old := executionWriter(t, s)
	type target struct {
		tenant      string
		session     sessions.Session
		environment sessions.Environment
		generation  string
	}
	var targets []target
	for range 33 {
		tenant, session, environment := connectionFixture(t, s)
		generation := uuid.NewString()
		if err := old.ReplaceEnvironmentConnection(t.Context(), tenant, environment.ID, generation); err != nil {
			t.Fatal(err)
		}
		if err := old.ObserveEnvironmentConnection(t.Context(), tenant, environment.ID, generation, 1, true); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target{tenant, session, environment, generation})
	}
	var killed bool
	if err := pool.QueryRow(t.Context(), "SELECT pg_terminate_backend($1,1000)", executionOwnerPID(t, pool)).Scan(&killed); err != nil || !killed {
		t.Fatal(killed, err)
	}
	next := executionWriter(t, s)
	first := targets[0]
	if err := old.ObserveEnvironmentConnection(t.Context(), first.tenant, first.environment.ID, first.generation, 2, false); err == nil {
		t.Fatal("lost owner wrote state")
	}
	if err := s.ReconcileEnvironmentConnections(t.Context()); err == nil {
		t.Fatal("unleased reconciliation accepted")
	}
	if err := next.ReconcileEnvironmentConnections(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		got, err := s.GetEnvironment(t.Context(), target.tenant, target.environment.ID)
		if err != nil || got.Status != "disconnected" {
			t.Fatal("old process remained connected", got, err)
		}
		changes := connectionChanges(t, s, target.tenant, target.session.ID)
		if len(changes) != 2 || changes[0].Event.Environment.Status != "connected" || changes[1].Event.Environment.Status != "disconnected" {
			t.Fatal("recovery lost event snapshots", changes)
		}
		before := connectionSnapshot(t, pool, target.environment.ID)
		if err := next.ObserveEnvironmentConnection(t.Context(), target.tenant, target.environment.ID, target.generation, 100, true); err != nil {
			t.Fatal(err)
		}
		if after := connectionSnapshot(t, pool, target.environment.ID); after != before {
			t.Fatal("old generation survived recovery")
		}
	}
	if err := next.ReconcileEnvironmentConnections(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(connectionChanges(t, s, first.tenant, first.session.ID)) != 2 {
		t.Fatal("repeated recovery duplicated a disconnect")
	}
	generation := uuid.NewString()
	if err := next.ReplaceEnvironmentConnection(t.Context(), first.tenant, first.environment.ID, generation); err != nil {
		t.Fatal(err)
	}
	if err := next.ObserveEnvironmentConnection(t.Context(), first.tenant, first.environment.ID, generation, 1, true); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetEnvironment(t.Context(), first.tenant, first.environment.ID)
	if err != nil || got.Status != "connected" {
		t.Fatal("new generation could not connect", got, err)
	}
}
