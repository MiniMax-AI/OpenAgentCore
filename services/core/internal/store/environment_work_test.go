package store_test

import (
	"errors"
	"testing"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
)

func TestEnvironmentInputWorkFiltersAndPagesDevices(t *testing.T) {
	h := newDispatchHarness(t)
	_, pool := store.NewTestStore(t)
	wanted := map[string]string{}
	for range 103 {
		pending := workerEnvironmentReservation(t, h)
		wanted[pending.ID] = pending.SessionID
	}
	for _, state := range []string{"expired", "cancelled", "deleted", "unbound", "revoked", "disconnected"} {
		pending := workerEnvironmentReservation(t, h)
		switch state {
		case "expired":
			makeEnvironmentExpiryDue(t, pool, &pending)
		case "cancelled":
			if _, err := h.s.CancelEnvironmentInput(t.Context(), h.tenant, pending.SessionID, pending.ID); err != nil {
				t.Fatal(err)
			}
		case "deleted":
			if err := h.s.DeleteSession(t.Context(), h.tenant, pending.SessionID); !errors.Is(err, store.ErrSessionNotIdle) {
				t.Fatal("pending input deleted", err)
			}
			if err := h.s.CommitLegacyDeletion(t.Context(), h.tenant, pending.SessionID); err != nil {
				t.Fatal(err)
			}
		case "unbound":
			wanted[pending.ID] = pending.SessionID
			if _, err := pool.Exec(t.Context(), "DELETE FROM session_devices WHERE session_id=$1", pending.SessionID); err != nil {
				t.Fatal(err)
			}
		default:
			runtime := h.environments[pending.SessionID]
			if state == "revoked" {
				if err := h.s.RevokeDevice(t.Context(), h.tenant, runtime.device.ID); err != nil {
					t.Fatal(err)
				}
				work, err := h.s.ListEnvironmentInputWork(t.Context(), "", []string{runtime.device.ID})
				if err != nil || len(work) != 0 {
					t.Fatal("revoked Runtime selected", work, err)
				}
			} else {
				_ = runtime.conn.Close()
				delete(h.environments, pending.SessionID)
			}
		}
	}
	for _, devices := range [][]string{nil, {}, {uuid.NewString()}} {
		work, err := h.s.ListEnvironmentInputWork(t.Context(), "", devices)
		if err != nil || len(work) != 0 {
			t.Fatal("unconnected work selected", work, err)
		}
	}
	unboundWorkerEnvironmentReservation(t, &dispatchHarness{s: h.s, tenant: uuid.NewString()})
	seen, cursor := 0, ""
	for _, count := range []int{100, 4, 0} {
		devices := []string{h.device.ID}
		for _, runtime := range h.environments {
			devices = append(devices, runtime.device.ID)
		}
		work, err := h.s.ListEnvironmentInputWork(t.Context(), cursor, devices)
		if err != nil || len(work) != count {
			t.Fatal("environment work page", len(work), count, err)
		}
		for _, item := range work {
			if item.TenantID != h.tenant || item.SessionID != wanted[item.ReservationID] || item.ReservationID <= cursor {
				t.Fatal("wrong scope or pagination", item)
			}
			cursor = item.ReservationID
			seen++
		}
	}
	if seen != len(wanted) {
		t.Fatal("pending work lost across pages")
	}
}
