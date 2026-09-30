package store

import (
	"context"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/db/sqlc"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestDeploymentObservationClockSampleFollowsRowLock(t *testing.T) {
	f := newProviderObservationFixture(t)
	turn := f.terminal(t, sessions.TurnCompleted, "", "")
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(t.Context(), "SELECT harness FROM deployment_model_providers FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := f.s.ObserveDeploymentModelProvider(t.Context(), f.tenant, f.session.ID, turn.ID)
		result <- err
	}()
	deadline := time.Now().Add(500 * time.Millisecond)
	waiting := false
	for time.Now().Before(deadline) {
		if err = f.pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%ObserveDeploymentModelProvider%')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !waiting {
		t.Fatal("observation did not acquire a lock wait")
	}
	var beforeUnlock time.Time
	if err = tx.QueryRow(t.Context(), "SELECT clock_timestamp()").Scan(&beforeUnlock); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	rows, err := f.s.ListDeploymentModelProviders(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].LastUsedAt == nil || rows[0].LastUsedAt.Before(beforeUnlock) {
		t.Fatal("receipt clock sampled before row lock")
	}
}

func TestDeploymentObservationWinningLockIsClearedByPUT(t *testing.T) {
	f := newProviderObservationFixture(t)
	turn := f.terminal(t, sessions.TurnCompleted, "", "")
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	lookup, _ := turnLookup(f.tenant, f.session.ID, turn.ID)
	n, err := sqlc.New(tx).ObserveDeploymentModelProvider(t.Context(), sqlc.ObserveDeploymentModelProviderParams{TenantID: lookup.TenantID, SessionID: lookup.SessionID, TurnID: lookup.ID})
	if err != nil || n != 1 {
		t.Fatal("observation did not win row lock", n, err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := f.s.SetDeploymentModelProvider(observationAdmin(t), "codex", v1.ModelConfigurationInput{ModelProvider: *f.input.ModelProvider, Model: "fixture"})
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		if err = f.pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%UpsertDeploymentModelProvider%')").Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !waiting {
		t.Fatal("PUT did not wait for observation")
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	rows, err := f.s.ListDeploymentModelProviders(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].LastUsedAt != nil || rows[0].LastErrorCode != nil || rows[0].LastErrorAt != nil {
		t.Fatal("PUT retained old revision observation")
	}
	f.observe(t, turn, 0)
}

func TestDeploymentObservationWaitingAndNonRootTurnCannotWrite(t *testing.T) {
	f := newProviderObservationFixture(t)
	receipt := submitMessage(t, f.s, f.tenant, f.session.ID, "waiting")
	f.observe(t, sessions.Turn{ID: receipt.TurnID}, 0)
	transition(t, f.s, f.tenant, f.session.ID, receipt.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	transition(t, f.s, f.tenant, f.session.ID, receipt.TurnID, sessions.TurnInProgress, sessions.TurnWaiting)
	f.observe(t, sessions.Turn{ID: receipt.TurnID}, 0)
	// Child Turn identifiers live outside turns. An absent root identifier is
	// rejected by the same SQL ownership join, without a child-history lookup.
	f.observe(t, sessions.Turn{ID: "ffffffff-ffff-4fff-bfff-ffffffffffff"}, 0)
}
