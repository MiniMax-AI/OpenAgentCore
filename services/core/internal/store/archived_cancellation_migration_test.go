package store

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestArchivedCancellationMigrationDoesNotAdoptOldRevocations(t *testing.T) {
	s, w, installation := managedArchiveFixture(t)
	tenant, session := managedArchiveSession(t, s, managerSessionInput(uuid.NewString()))
	owner := archiveAllocation(t, w, tenant, session, installation)
	input := submitMessage(t, s, tenant, session.ID, "waiting")
	transition(t, w, tenant, session.ID, input.TurnID, sessions.TurnQueued, sessions.TurnInProgress)
	transition(t, w, tenant, session.ID, input.TurnID, sessions.TurnInProgress, sessions.TurnWaiting)
	if _, err := w.ArchiveManagedSession(adminDeleteContext(t.Context(), tenant, uuid.NewString()), tenant, session.ID, 1); err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(stdlib.GetConnector(*s.pool.Config().ConnConfig))
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"), goose.WithTableName("agents_api_schema_version"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(t.Context(), 79); err == nil {
		t.Fatal("downgrade discarded unsettled cancellation marker")
	} else if !strings.Contains(err.Error(), "Cannot remove archived cancellation receipts while cleanup is unsettled") {
		t.Fatal("downgrade failed before checking the cancellation marker", err)
	}
	// Later migrations can already have been reverted before migration 80
	// refuses. Restore the current schema before using current generated queries.
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := deploymentExecution(t, w).SettleCreation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := deploymentExecution(t, w).ReleaseAllocation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(t.Context(), 79); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	var marker *string
	if err := s.pool.QueryRow(t.Context(), "SELECT archive_cancel_turn_id::text FROM devices WHERE id=$1", owner.DeviceID).Scan(&marker); err != nil || marker != nil {
		t.Fatal("upgrade adopted historical revoked device", marker, err)
	}
}
