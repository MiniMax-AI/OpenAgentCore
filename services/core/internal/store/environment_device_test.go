package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
)

func TestWorkerEnvironmentSelectsCapableDeviceWithoutMovingBinding(t *testing.T) {
	for _, missing := range []string{"preparation", "local_environment", "durable_input_receipts"} {
		t.Run(missing, func(t *testing.T) {
			h := newDispatchHarness(t)
			_, pool := store.NewTestStore(t)
			enableWorkerEnvironment(t, h)
			pending := unboundWorkerEnvironmentReservation(t, h)
			bound := workerEnvironmentReservation(t, h)
			originalRuntime := h.environments[bound.SessionID]
			caps := workerEnvironmentCapabilities()
			caps.Preparation = proto.CapabilityFromBool(missing != "preparation")
			caps.LocalEnvironment = proto.CapabilityFromBool(missing != "local_environment")
			caps.DurableInputReceipts = proto.CapabilityFromBool(missing != "durable_input_receipts")
			awaitFixtureCapabilities(t, originalRuntime, caps)
			generalFrames := workerFrames(t, h)
			boundFrames := workerFrames(t, originalRuntime)
			_, stop := startEnvironmentExpiryWorker(t, h.d)
			select {
			case frame := <-generalFrames:
				t.Fatal("general device received self-hosted work", frame.Type)
			case frame := <-boundFrames:
				t.Fatal("incapable enrolled device received work", frame.Type)
			case <-time.After(time.Second):
			}
			if _, err := h.s.GetSessionDevice(t.Context(), h.tenant, pending.SessionID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("unregistered Runtime was assigned general compute", err)
			}
			session, err := h.s.GetSession(t.Context(), h.tenant, pending.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			other := connectFixtureRuntime(t, h, session)
			otherFrames := workerFrames(t, other)
			request := nextWorkerFrame(t, otherFrames, proto.TypeExecutionPrepare)
			selected, err := h.s.GetSessionDevice(t.Context(), h.tenant, pending.SessionID)
			if err != nil || selected.ID != other.device.ID {
				t.Fatal("enrollment did not retain exact Runtime", err)
			}
			original, err := h.s.GetSessionDevice(t.Context(), h.tenant, bound.SessionID)
			if err != nil || original.ID != originalRuntime.device.ID {
				t.Fatal("existing binding moved to a capable Runtime", err)
			}
			handle := acknowledgePreparation(other, request.ID)
			other.write(request.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "failed"})
			release := nextWorkerFrame(t, otherFrames, proto.TypeExecutionRelease)
			var released proto.ExecutionReleasePayload
			if release.ID != request.ID || release.DecodePayload(&released) != nil || released.Handle != handle {
				t.Fatal("preparation owner was not released")
			}
			stop()
			for _, value := range []store.EnvironmentInputReservation{pending, bound} {
				stored, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, value.SessionID, value.ID)
				if err != nil || stored.State != store.EnvironmentInputPending || !stored.Deadline.Equal(value.Deadline) {
					t.Fatal("device readiness changed pending input", err)
				}
				assertEnvironmentExpiryHasNoHistory(t, pool, value.SessionID)
			}
		})
	}
}
