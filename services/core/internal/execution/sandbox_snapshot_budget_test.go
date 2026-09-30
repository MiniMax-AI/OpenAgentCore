package execution

import (
	"context"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type snapshotBudgetKey struct{}
type snapshotBudgetQuery struct {
	started time.Time
	ordinal int64
}
type snapshotBudget struct {
	t     *testing.T
	armed atomic.Bool
	reads atomic.Int64
}

func (d *snapshotBudget) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	q := snapshotBudgetQuery{started: time.Now()}
	if d.armed.Load() && strings.Contains(data.SQL, "GetSandboxDeploymentSnapshot") && strings.Contains(string(debug.Stack()), "runtimeManager).resetPage") {
		q.ordinal = d.reads.Add(1)
	}
	return context.WithValue(ctx, snapshotBudgetKey{}, q)
}
func (d *snapshotBudget) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	q, _ := ctx.Value(snapshotBudgetKey{}).(snapshotBudgetQuery)
	if q.ordinal == 0 {
		return
	}
	d.t.Logf("snapshot=%d query_elapsed=%s err=%v ctx=%v", q.ordinal, time.Since(q.started), data.Err, ctx.Err())
	// Model scheduling pressure on the first two reads without changing the real
	// five-second page deadline. The final snapshot uses the lease connection.
	if q.ordinal <= 2 && data.Err == nil {
		delay := 2200*time.Millisecond - time.Since(q.started)
		if delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
			}
		}
	}
}

func TestSandboxResetSnapshotFitsPageBudget(t *testing.T) {
	budget := &snapshotBudget{t: t}
	s, lease := resetManagerStoreConfig(t, func(cfg *pgxpool.Config) {
		cfg.ConnConfig.RuntimeParams["jit"] = "on"
		cfg.ConnConfig.Tracer = budget
	})
	w := lease.Store()
	id := uuid.NewString()
	if err := w.ClaimWebSandboxDeployment(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	selection := store.SandboxDeploymentSetupRequest{Provider: "e2b", E2B: &sandbox.E2BConfiguration{APIKey: "fixture-key", Template: "runtime:" + uuid.NewString()}}
	selection.Resources.CPUs = 2
	selection.Resources.MemoryMiB = 2048
	if _, err := w.InitializeSandboxDeployment(t.Context(), id, selection); err != nil {
		t.Fatal(err)
	}
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	configuration := NewDeferredRuntimeProvider(id, func(ctx context.Context) (*RuntimeProvider, error) {
		setup, err := s.GetSandboxSetup(ctx)
		if err != nil || setup.Provider == "" {
			return nil, err
		}
		return &RuntimeProvider{InstallationID: id, ProviderKind: setup.Provider, Generation: setup.Generation, Mode: setup.Mode, CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: hub.Proxy(uuid.NewString(), "docker", 1)}, nil
	})
	m, err := newRuntimeManager(w, gateway.NewRegistry(), configuration)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.stop(); m.drain() }()
	if _, err = m.ensureDeployment(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = w.StartSandboxReset(adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "reset-test", ActorLabel: "operator", RequestID: "request", TraceID: "trace"}), id, store.SandboxResetRequest{ExpectedGeneration: 1, Clear: "force"}); err != nil {
		t.Fatal(err)
	}
	budget.armed.Store(true)
	started := time.Now()
	err = m.resetStep(t.Context())
	ping := lease.Ping(t.Context())
	t.Logf("reset_elapsed=%s reset_error=%v lease_ping=%v", time.Since(started), err, ping)
	if err != nil || ping != nil {
		t.Fatal("bounded snapshot lost execution ownership", err, ping)
	}
	if budget.reads.Load() < 3 {
		t.Fatal("did not exercise final leased snapshot")
	}
	if err := w.CollectSandboxGenerations(t.Context()); err != nil {
		t.Fatal("next generation collection lost ownership", err)
	}
	view, err := s.GetRuntimeDeployment(t.Context())
	if err != nil || view.Generation != 2 || view.Provider != "" || view.Reset != nil {
		t.Fatal("reset did not commit", view, err)
	}
}
