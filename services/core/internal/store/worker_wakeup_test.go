package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type schedulerQueryOrder struct {
	armed atomic.Bool
	first chan string
}

func (trace *schedulerQueryOrder) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	for _, name := range []string{"ListExecutionWork", "ListDueEnvironmentInputs"} {
		if strings.HasPrefix(data.SQL, "-- name: "+name+" ") && trace.armed.CompareAndSwap(true, false) {
			trace.first <- name
		}
	}
	return ctx
}
func (*schedulerQueryOrder) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestWorkerSchedulerCommittedAdmissionWakesBeforeMaintenance(t *testing.T) {
	for _, operation := range []string{"submit", "create", "stream", "rejected"} {
		t.Run(operation, func(t *testing.T) {
			h := newDispatchHarness(t)
			enableWorkerEnvironment(t, h)
			h.session = publicSession(t, h, "wakeup")
			_, pool := store.NewTestStore(t)
			trace := &schedulerQueryOrder{first: make(chan string, 1)}
			config := pool.Config()
			config.ConnConfig.Tracer = trace
			instrumented, err := pgxpool.NewWithConfig(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			defer instrumented.Close()
			h.d.Store = store.NewWithCredentialCipher(instrumented, store.FixtureCipher())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			worker := startWorker(t, ctx, fixtureDB{pool: instrumented, cipher: store.FixtureCipher()}, h.d)
			done := make(chan error, 1)
			started := false
			defer func() {
				cancel()
				if !started {
					go func() { done <- worker.Run(ctx) }()
				}
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("worker did not stop")
				}
			}()
			input := []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"wake"}`)}}
			switch operation {
			case "submit":
				_, err = worker.SubmitInputs(ctx, h.tenant, h.session.ID, "wake", input)
			case "create", "stream":
				creation := store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "wake", Configuration: h.session.Configuration, InitialInputs: input}
				if operation == "create" {
					_, err = worker.CreateSession(ctx, h.tenant, creation)
				} else {
					_, err = worker.CreateSessionStream(ctx, h.tenant, creation)
				}
			case "rejected":
				h.message("wake", "different")
				_, err = worker.SubmitInputs(ctx, h.tenant, h.session.ID, "wake", input)
				if err == nil {
					t.Fatal("changed retry was accepted")
				}
				err = nil
			}
			if err != nil {
				t.Fatal(err)
			}
			trace.armed.Store(true)
			started = true
			go func() { done <- worker.Run(ctx) }()
			// Maintenance always starts with expiry. A committed admission must
			// select work first, without depending on a short timing threshold.
			select {
			case first := <-trace.first:
				want := "ListExecutionWork"
				if operation == "rejected" {
					want = "ListDueEnvironmentInputs"
				}
				if first != want {
					t.Fatalf("first scheduler query = %s, want %s", first, want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not schedule")
			}
		})
	}
}

func TestWorkerSchedulerHintBypassesEnvironmentScanThrottle(t *testing.T) {
	h := newDispatchHarnessForSession(t, []byte(`{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`), false)
	enableWorkerEnvironment(t, h)
	frames := workerFrames(t, h)
	worker, stop := startEnvironmentExpiryWorker(t, h.db, h.d)
	defer stop()
	awaitDaemonRemoteCondition(t, t.Context(), 5*time.Second, "initial empty scheduler scan", func() bool {
		return worker.MetricsSnapshot().Scheduler.LastRunAt != nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	admitted := make(chan error, 1)
	started := time.Now()
	go func() {
		_, err := worker.SubmitInputs(ctx, h.tenant, h.session.ID, "wake", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"wake"}`)}})
		admitted <- err
	}()
	prepare := nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
	if elapsed := time.Since(started); elapsed >= 750*time.Millisecond {
		t.Fatalf("committed input waited for the environment scan interval: %s", elapsed)
	}
	handle := acknowledgePreparation(h, prepare.ID)
	h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "failed"})
	nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
	cancel()
	select {
	case <-admitted:
	case <-time.After(5 * time.Second):
		t.Fatal("detached admission waiter did not stop")
	}
}
