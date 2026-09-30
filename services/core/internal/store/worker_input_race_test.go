package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Intercept the actual database read after candidate selection. The concurrent
// mutation uses a separate pool and the production cancellation/deletion path.
type beforeInputRead struct {
	once sync.Once
	run  func()
}

func (h *beforeInputRead) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "-- name: ListTurnInputs ") {
		h.once.Do(h.run)
	}
	return ctx
}
func (*beforeInputRead) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestWorkerInputReadSkipsConcurrentlyCancelledCandidate(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		name := "cancel"
		if deleted {
			name = "delete"
		}
		t.Run(name, func(t *testing.T) {
			h := newDispatchHarness(t)
			candidate := h.message("candidate", "queued")
			candidateSession := h.session.ID
			_, pool := store.NewTestStore(t)
			cfg := pool.Config()
			mutated := make(chan error, 1)
			cfg.ConnConfig.Tracer = &beforeInputRead{run: func() {
				if deleted {
					// Public deletion now rejects the queued candidate; an
					// earlier release's marker must still fence its execution.
					mutated <- h.s.CommitLegacyDeletion(t.Context(), h.tenant, candidateSession)
					return
				}
				_, err := h.s.SubmitInputs(t.Context(), h.tenant, candidateSession, "cancel", []store.Input{{Kind: "cancel", Payload: json.RawMessage(`{}`)}})
				mutated <- err
			}}
			instrumented, err := pgxpool.NewWithConfig(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer instrumented.Close()
			h.d.Store = store.New(instrumented)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			worker := startWorker(t, ctx, fixtureDB{pool: instrumented}, h.d)
			done := make(chan error, 1)
			go func() { done <- worker.Run(ctx) }()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("worker did not stop")
				}
			}()
			select {
			case err := <-mutated:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("input-read interleaving was not reached")
			}
			turn, err := h.s.GetTurn(ctx, h.tenant, candidateSession, candidate.TurnID)
			if err != nil || turn.Status != sessions.TurnCancelled {
				t.Fatal("candidate was not cancelled", err)
			}
			// A later Session must still execute through this same Worker.
			h.session, err = h.s.CreateSession(ctx, h.tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "healthy", Configuration: h.session.Configuration})
			if err != nil {
				t.Fatal(err)
			}
			healthy := h.message("healthy", "continue")
			select {
			case err := <-done:
				// Keep teardown able to consume the already completed Worker.
				done <- err
				t.Fatal("a stale candidate terminated the global Worker", err)
			case <-time.After(650 * time.Millisecond):
			}
			frame := h.read(testExecutionRequest)
			if frame.ID != healthy.TurnID {
				t.Fatal("cancelled candidate reached the Runtime", frame.ID)
			}
			h.write(healthy.TurnID, proto.TypeDone, proto.DonePayload{Content: "continued"})
			waitTurn(t, h, healthy.TurnID, sessions.TurnCompleted)
		})
	}
}
