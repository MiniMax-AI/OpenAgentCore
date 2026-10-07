package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

func TestEnvironmentExpirySerializesWithTargetedSettlement(t *testing.T) {
	for _, action := range []string{"promote", "cancel", "delete"} {
		t.Run(action, func(t *testing.T) {
			s, pool := testStore(t)
			tenant, session := environmentInputSession(t, s)
			pending := reserveEnvironmentInput(t, s, tenant, session.ID, "pending")
			if _, err := pool.Exec(t.Context(), "UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1", pending.ID); err != nil {
				t.Fatal(err)
			}
			operations := sessionExecution(t, executionWriter(t, s).lease)
			start := make(chan struct{})
			results := make(chan error, 2)
			go func() {
				<-start
				_, err := operations.ExpireEnvironmentInputs(t.Context())
				results <- err
			}()
			go func() {
				<-start
				var err error
				switch action {
				case "promote":
					_, err = operations.PromoteEnvironmentInput(t.Context(), tenant, session.ID, pending.ID)
				case "cancel":
					_, err = cancelEnvironmentInput(t.Context(), s, tenant, session.ID, pending.ID)
				case "delete":
					err = sessionService(t, s).DeleteSession(t.Context(), sessions.DeleteSessionCommand{TenantID: tenant, SessionID: session.ID})
				}
				results <- err
			}()
			close(start)
			for range 2 {
				if err := <-results; err != nil {
					t.Fatal(err)
				}
			}
			var state string
			if err := pool.QueryRow(t.Context(), "SELECT state FROM environment_input_reservations WHERE id=$1", pending.ID).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != sessions.EnvironmentInputExpired && (action != "delete" || state != sessions.EnvironmentInputCancelled) {
				t.Fatal("invalid competing settlement", state)
			}
			environmentInputHistory(t, pool, session.ID, 0, 0)
			if action == "delete" {
				if _, err := operations.PromoteEnvironmentInput(t.Context(), tenant, session.ID, pending.ID); !errors.Is(err, sessions.ErrNotFound) {
					t.Fatal("deleted input resurrected", err)
				}
				return
			}
			later := reserveEnvironmentInput(t, s, tenant, session.ID, uuid.NewString())
			for _, settle := range []func(context.Context, string, string, string) (sessions.EnvironmentInputReservation, error){
				operations.PromoteEnvironmentInput,
				sessionService(t, s).ExpireEnvironmentInput,
			} {
				old, err := settle(t.Context(), tenant, session.ID, pending.ID)
				if err != nil || old.State != state {
					t.Fatal("old reservation changed", old, err)
				}
			}
			if _, err := operations.ExpireEnvironmentInputs(t.Context()); err != nil {
				t.Fatal(err)
			}
			got, err := sessionAdapter(s).GetEnvironmentInputReservation(t.Context(), tenant, session.ID, later.ID)
			if err != nil || got.State != sessions.EnvironmentInputPending || !got.Deadline.Equal(later.Deadline) {
				t.Fatal("old settlement affected successor", got, err)
			}
		})
	}
}
