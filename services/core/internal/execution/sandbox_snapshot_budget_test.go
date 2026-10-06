package execution

import (
	"context"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/adminaudit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/docker"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/node"
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
	// Model scheduling pressure on both page reads without changing the real
	// five-second page deadline. Completion publishes the committed generation
	// without a third read.
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
	_, owner, deployments, reader := resetManagerStoreConfig(t, func(cfg *pgxpool.Config) {
		cfg.ConnConfig.RuntimeParams["jit"] = "on"
		cfg.ConnConfig.Tracer = budget
	})
	id := initializeE2BDeployment(t, owner)
	hub := node.NewHub(node.HubOptions{})
	defer hub.Close()
	configuration := NewDeferredRuntimeProvider(id, func(ctx context.Context) (*RuntimeProvider, error) {
		setup, err := deployments.Setup(ctx)
		if err != nil || setup.Provider == "" {
			return nil, err
		}
		return &RuntimeProvider{InstallationID: id, ProviderKind: setup.Provider, Generation: setup.Generation, Mode: setup.Mode, CoreURL: "https://core.example/api/v1", BackendFingerprint: setup.BackendFingerprint, Provider: hub.Proxy(uuid.NewString(), "docker", docker.Operations(), 1)}, nil
	})
	m, err := newRuntimeManager(owner, deployments, reader, nil, runtimegateway.NewRegistry(), configuration)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { m.stop(); m.drain() }()
	if _, err = m.ensureDeployment(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = owner.Deployment.StartReset(adminaudit.WithSource(t.Context(), adminaudit.Source{CredentialID: "reset-test", ActorLabel: "operator", RequestID: "request", TraceID: "trace"}), id, deployment.ResetRequest{ExpectedGeneration: 1, Clear: deployment.ResetForce}); err != nil {
		t.Fatal(err)
	}
	budget.armed.Store(true)
	started := time.Now()
	err = m.resetStep(t.Context())
	ping := owner.Lease.CheckOwnership(t.Context())
	t.Logf("reset_elapsed=%s reset_error=%v lease_ping=%v", time.Since(started), err, ping)
	if err != nil || ping != nil {
		t.Fatal("bounded snapshot lost execution ownership", err, ping)
	}
	if reads := budget.reads.Load(); reads != 2 {
		t.Fatal("reset page read the deployment", reads, "times, want the two bounded reads before completion")
	}
	if err := owner.Deployment.CollectGenerations(t.Context()); err != nil {
		t.Fatal("next generation collection lost ownership", err)
	}
	view, err := deployments.View(t.Context())
	if err != nil || view.Generation != 2 || view.Provider != "" || view.Reset != nil {
		t.Fatal("reset did not commit", view, err)
	}
}
