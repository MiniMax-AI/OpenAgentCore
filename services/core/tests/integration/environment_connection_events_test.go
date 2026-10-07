package integration

import (
	"context"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI/OpenAgentCore/contracts/agents-api/v1"
)

func awaitEnvironmentConnectionState(t *testing.T, ctx context.Context, s *Store, tenant, environment, status string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		value, err := sessionAdapter(s).GetEnvironment(ctx, tenant, environment)
		if err != nil {
			t.Fatal(err)
		}
		if value.Status == status {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatal("persisted connection state did not converge", status)
}

func retainedEnvironmentEvents(t *testing.T, ctx context.Context, s *Store, tenant, session, environment string) []v1.SessionEvent {
	t.Helper()
	var after int64
	events := []v1.SessionEvent{}
	for {
		changes, err := sessionAdapter(s).ListSessionEvents(ctx, tenant, session, after)
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) == 0 {
			break
		}
		for _, change := range changes {
			after = change.Sequence
			if change.Event.Environment == nil {
				continue
			}
			event := change.Event
			if event.SessionID != session || event.Environment.ID != environment || event.Environment.Type != "self_hosted" || event.Environment.Error != nil || event.TurnID != "" {
				t.Fatal("invalid transport event identity")
			}
			status := event.Environment.Status
			if (status != "connected" && status != "disconnected") || event.Type != "agent.session.environment."+status {
				t.Fatal("transport observation claimed native readiness")
			}
			if len(events) > 0 && events[len(events)-1].Type == event.Type {
				t.Fatal("duplicate lifecycle transition")
			}
			events = append(events, event)
		}
	}
	return events
}
