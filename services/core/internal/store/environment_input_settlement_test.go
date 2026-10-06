package store

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestEnvironmentInputDeletionSettlesPendingAndFencesPromotion(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "racing-promotion"}[concurrent], func(t *testing.T) {
			s, pool := testStore(t)
			writer := executionWriter(t, s)
			tenant, session := environmentInputSession(t, s)
			ctx := context.Background()
			pending := reserveEnvironmentInput(t, s, tenant, session.ID, "pending")
			done := make(chan error, 1)
			if concurrent {
				go func() {
					_, err := sessionExecution(t, writer.lease).PromoteEnvironmentInput(ctx, tenant, session.ID, pending.ID)
					done <- err
				}()
			}
			if !concurrent {
				if err := sessionService(t, s).DeleteSession(ctx, sessions.DeleteSessionCommand{TenantID: tenant, SessionID: session.ID}); !errors.Is(err, sessions.ErrNotIdle) {
					t.Fatal("pending input deleted", err)
				}
			}
			// A marker from an earlier release still settles and fences the input.
			if err := s.commitLegacyDeletion(ctx, tenant, session.ID); err != nil {
				t.Fatal(err)
			}
			if concurrent {
				if err := <-done; err != nil && !errors.Is(err, sessions.ErrNotFound) {
					t.Fatal(err)
				}
			}
			for _, action := range []func(context.Context, string, string, string) (sessions.EnvironmentInputReservation, error){
				sessionAdapter(s).GetEnvironmentInputReservation,
				sessionExecution(t, writer.lease).PromoteEnvironmentInput,
				func(ctx context.Context, tenant, session, id string) (sessions.EnvironmentInputReservation, error) {
					return cancelEnvironmentInput(ctx, s, tenant, session, id)
				},
			} {
				if _, err := action(ctx, tenant, session.ID, pending.ID); !errors.Is(err, sessions.ErrNotFound) {
					t.Fatal("deleted reservation remained accessible", err)
				}
			}
			if _, err := sessionService(t, s).ReserveEnvironmentInput(ctx, tenant, session.ID, "late", pending.Inputs); !errors.Is(err, sessions.ErrNotFound) {
				t.Fatal("deleted Session accepted reservation", err)
			}
			var state string
			var active int
			if err := pool.QueryRow(ctx, "SELECT state FROM environment_input_reservations WHERE id=$1", pending.ID).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != sessions.EnvironmentInputCancelled && (!concurrent || state != sessions.EnvironmentInputAdmitted) {
				t.Fatal("deletion lost pending settlement", state)
			}
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM turns WHERE session_id=$1 AND (status='queued' OR (status IN ('in_progress','waiting') AND cancel_requested_at IS NULL))", session.ID).Scan(&active); err != nil || active != 0 {
				t.Fatal("deleted reservation retained unclaimed or uncancelled work", active, err)
			}
		})
	}
}
