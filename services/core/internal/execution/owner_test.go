package execution

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgtest"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/google/uuid"
)

// closeCountingLease counts Close calls on the lease a Worker owns. It forwards
// to inner; without inner, any call other than Close fails the test.
type closeCountingLease struct {
	t        *testing.T
	inner    Ownership
	closable atomic.Bool
	closes   atomic.Int32
}

func (l *closeCountingLease) CheckOwnership(ctx context.Context) error {
	if l.inner == nil {
		l.t.Error("unexpected call to CheckOwnership")
		return errors.New("unexpected call to CheckOwnership")
	}
	return l.inner.CheckOwnership(ctx)
}

func (l *closeCountingLease) CancelOperations(ctx context.Context, cancel context.CancelFunc) error {
	if l.inner == nil {
		l.t.Error("unexpected call to CancelOperations")
		return errors.New("unexpected call to CancelOperations")
	}
	return l.inner.CancelOperations(ctx, cancel)
}

func (l *closeCountingLease) Close(ctx context.Context) error {
	l.closes.Add(1)
	if !l.closable.Load() {
		l.t.Error("lease closed before its owner finished")
	}
	if _, bounded := ctx.Deadline(); !bounded || ctx.Err() != nil {
		l.t.Error("lease closed without a live bounded context", ctx.Err())
	}
	if l.inner == nil {
		return nil
	}
	return l.inner.Close(ctx)
}

// unusedSessions returns the Session service and reader for Workers that fail
// to start. Any call through them panics.
func unusedSessions(t *testing.T) (*sessions.Service, sessions.Reader) {
	t.Helper()
	service, err := sessions.NewService(struct{ sessions.Storage }{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return service, struct{ sessions.Reader }{}
}

// unusedObserver fails the test on any observation. The Workers it serves run
// no Turn.
type unusedObserver struct{ t *testing.T }

func (o unusedObserver) ObserveDeploymentModelProvider(context.Context, modelconfiguration.Observation) (int64, error) {
	o.t.Error("unexpected call to ObserveDeploymentModelProvider")
	return 0, errors.New("unexpected call to ObserveDeploymentModelProvider")
}

func TestStartWorkerFailureClosesLeaseOnce(t *testing.T) {
	if _, err := StartWorker(t.Context(), &Dispatcher{}, Owner{}); err == nil {
		t.Fatal("worker started without an execution lease")
	}
	// The failed request's context is already canceled; the close must not inherit it.
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for name, start := range map[string]func(*testing.T, *closeCountingLease) error{
		"negative concurrency": func(t *testing.T, lease *closeCountingLease) error {
			_, err := StartWorker(canceled, &Dispatcher{MaxConcurrentExecutions: -1}, Owner{Lease: lease})
			return err
		},
		"excess concurrency": func(t *testing.T, lease *closeCountingLease) error {
			_, err := StartWorker(canceled, &Dispatcher{MaxConcurrentExecutions: 1025}, Owner{Lease: lease})
			return err
		},
		"missing Credentials": func(t *testing.T, lease *closeCountingLease) error {
			_, err := StartWorker(canceled, &Dispatcher{Observer: unusedObserver{t}}, Owner{Lease: lease})
			return err
		},
		"missing observer": func(t *testing.T, lease *closeCountingLease) error {
			_, err := StartWorker(canceled, &Dispatcher{Credentials: &recordingCredentials{}}, Owner{Lease: lease})
			return err
		},
		"missing deployment service": func(t *testing.T, lease *closeCountingLease) error {
			_, err := StartWorker(canceled, &Dispatcher{Credentials: &recordingCredentials{}, Observer: unusedObserver{t}}, Owner{Lease: lease})
			return err
		},
		"missing deployment reader": func(t *testing.T, lease *closeCountingLease) error {
			_, deployments, _ := resetManager(t)
			_, err := StartWorker(canceled, &Dispatcher{Credentials: &recordingCredentials{}, Observer: unusedObserver{t}, Deployment: deployments}, Owner{Lease: lease})
			return err
		},
		"missing Session service": func(t *testing.T, lease *closeCountingLease) error {
			_, deployments, deploymentReader := resetManager(t)
			_, err := StartWorker(canceled, &Dispatcher{Credentials: &recordingCredentials{}, Observer: unusedObserver{t}, Deployment: deployments, DeploymentReader: deploymentReader}, Owner{Lease: lease})
			return err
		},
		"missing Session reader": func(t *testing.T, lease *closeCountingLease) error {
			_, deployments, deploymentReader := resetManager(t)
			service, _ := unusedSessions(t)
			_, err := StartWorker(canceled, &Dispatcher{Credentials: &recordingCredentials{}, Observer: unusedObserver{t}, Deployment: deployments, DeploymentReader: deploymentReader, Sessions: service}, Owner{Lease: lease})
			return err
		},
		"missing runtime provider": func(t *testing.T, lease *closeCountingLease) error {
			_, deployments, deploymentReader := resetManager(t)
			service, reader := unusedSessions(t)
			_, err := StartWorker(canceled, &Dispatcher{Credentials: &recordingCredentials{}, Observer: unusedObserver{t}, Deployment: deployments, DeploymentReader: deploymentReader, Sessions: service, SessionsReader: reader}, Owner{Lease: lease})
			return err
		},
		"missing Session operations": func(t *testing.T, lease *closeCountingLease) error {
			_, deployments, deploymentReader := resetManager(t)
			service, reader := unusedSessions(t)
			_, err := StartWorker(canceled, &Dispatcher{Credentials: &recordingCredentials{}, Observer: unusedObserver{t}, Deployment: deployments, DeploymentReader: deploymentReader, Sessions: service, SessionsReader: reader, Providers: &fakeProviderRegistry{t: t}, NodeProviders: fixtureNodeProviders{t: t}, InstallationID: uuid.NewString()}, Owner{Lease: lease})
			return err
		},
		"missing deployment": func(t *testing.T, lease *closeCountingLease) error {
			owner, deployments, deploymentReader := resetManager(t)
			service, reader := unusedSessions(t)
			_, err := StartWorker(canceled, &Dispatcher{Credentials: &recordingCredentials{}, Observer: unusedObserver{t}, Deployment: deployments, DeploymentReader: deploymentReader, Sessions: service, SessionsReader: reader, Providers: &fakeProviderRegistry{t: t}, NodeProviders: fixtureNodeProviders{t: t}, InstallationID: uuid.NewString()}, Owner{Lease: lease, Sessions: owner.Sessions})
			return err
		},
		"deployment claim": func(t *testing.T, lease *closeCountingLease) error {
			owner, deployments, deploymentReader := resetManager(t)
			lease.inner = owner.Lease
			service, reader := unusedSessions(t)
			dispatcher := &Dispatcher{Registry: runtimegateway.NewRegistry(), Credentials: &recordingCredentials{}, Observer: unusedObserver{t}, Deployment: deployments, DeploymentReader: deploymentReader, Sessions: service, SessionsReader: reader, Links: relay.New(nil), Providers: &fakeProviderRegistry{t: t}, NodeProviders: fixtureNodeProviders{t: t}, InstallationID: uuid.NewString()}
			_, err := StartWorker(canceled, dispatcher, Owner{Lease: lease, Deployment: owner.Deployment, Sessions: owner.Sessions})
			if ping := owner.Lease.CheckOwnership(t.Context()); !errors.Is(ping, pgunit.ErrLeaseClosed) {
				t.Error("failed start kept the database lease", ping)
			}
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			lease := &closeCountingLease{t: t}
			lease.closable.Store(true)
			if err := start(t, lease); err == nil {
				t.Fatal("worker started")
			}
			if closes := lease.closes.Load(); closes != 1 {
				t.Fatal("failed start closed the lease", closes, "times")
			}
		})
	}
}

func TestStartWorkerChecksDeploymentAfterItsDependencies(t *testing.T) {
	owner, deployments, deploymentReader := resetManager(t)
	credentials, observer := &recordingCredentials{}, unusedObserver{t}
	service, reader := unusedSessions(t)
	bound := Owner{Sessions: owner.Sessions}
	for _, test := range []struct {
		name       string
		dispatcher Dispatcher
		owner      Owner
		want       string
	}{
		{"missing Credentials", Dispatcher{Observer: observer}, bound, "execution worker requires MCP Credentials"},
		{"missing observer", Dispatcher{Credentials: credentials}, bound, "execution requires a model configuration observer"},
		{"missing deployment service", Dispatcher{Credentials: credentials, Observer: observer}, bound, "execution worker requires the deployment service"},
		{"missing deployment reader", Dispatcher{Credentials: credentials, Observer: observer, Deployment: deployments}, bound, "execution worker requires the deployment reader"},
		{"missing Session service", Dispatcher{Credentials: credentials, Observer: observer, Deployment: deployments, DeploymentReader: deploymentReader}, bound, "execution worker requires the Session service"},
		{"missing Session reader", Dispatcher{Credentials: credentials, Observer: observer, Deployment: deployments, DeploymentReader: deploymentReader, Sessions: service}, bound, "execution worker requires the Session reader"},
		{"missing runtime provider", Dispatcher{Credentials: credentials, Observer: observer, Deployment: deployments, DeploymentReader: deploymentReader, Sessions: service, SessionsReader: reader}, bound, "execution worker requires the sandbox runtime provider"},
		{"missing Session operations", Dispatcher{Credentials: credentials, Observer: observer, Deployment: deployments, DeploymentReader: deploymentReader, Sessions: service, SessionsReader: reader, Providers: &fakeProviderRegistry{t: t}}, Owner{}, "execution requires the Session execution operations"},
		{"missing deployment", Dispatcher{Credentials: credentials, Observer: observer, Deployment: deployments, DeploymentReader: deploymentReader, Sessions: service, SessionsReader: reader, Providers: &fakeProviderRegistry{t: t}}, bound, "execution worker requires the deployment execution operations"},
		{"missing Link relay", Dispatcher{Credentials: credentials, Observer: observer, Deployment: deployments, DeploymentReader: deploymentReader, Sessions: service, SessionsReader: reader, Providers: &fakeProviderRegistry{t: t}}, Owner{Sessions: owner.Sessions, Deployment: owner.Deployment}, "execution worker requires the Link relay"},
	} {
		t.Run(test.name, func(t *testing.T) {
			lease := &closeCountingLease{t: t}
			lease.closable.Store(true)
			started := test.owner
			started.Lease = lease
			if _, err := StartWorker(t.Context(), &test.dispatcher, started); err == nil || err.Error() != test.want {
				t.Fatalf("StartWorker without deployment operations = %v, want %q", err, test.want)
			}
		})
	}
}

func TestWorkerRunClosesLeaseAfterDrain(t *testing.T) {
	owner, deployments, deploymentReader, pool := resetManagerDB(t, nil)
	lease := &closeCountingLease{t: t, inner: owner.Lease}
	// The Worker's first reconciliation scans the Session work.
	reader, service := testSessions(t, pool, pgtest.CredentialKey(t))
	dispatcher := &Dispatcher{Registry: runtimegateway.NewRegistry(), Credentials: &recordingCredentials{}, Observer: unusedObserver{t}, Deployment: deployments, DeploymentReader: deploymentReader, Sessions: service, SessionsReader: reader, Links: relay.New(nil), Providers: &fakeProviderRegistry{t: t}, NodeProviders: fixtureNodeProviders{t: t}, InstallationID: uuid.NewString()}
	worker, err := StartWorker(t.Context(), dispatcher, Owner{Lease: lease, Deployment: owner.Deployment, Sessions: owner.Sessions})
	if err != nil {
		t.Fatal(err)
	}
	// An external provisioning caller is still in flight when Run exits.
	worker.runtimes.active.Add(1)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	cancel()
	select {
	case <-worker.runtimes.ctx.Done():
	case <-time.After(5 * time.Second):
		worker.runtimes.active.Done()
		t.Fatal("Run did not stop its runtimes")
	}
	lease.closable.Store(true)
	worker.runtimes.active.Done()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit after draining")
	}
	if closes := lease.closes.Load(); closes != 1 {
		t.Fatal("Run closed the lease", closes, "times")
	}
	if ping := owner.Lease.CheckOwnership(t.Context()); !errors.Is(ping, pgunit.ErrLeaseClosed) {
		t.Fatal("Run kept the database lease", ping)
	}
}
