package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxbootstrap"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimedevice"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// RuntimeProvider binds one deployment to one sandbox installation.
// BackendFingerprint identifies its namespace independently of mutable sizing.
type RuntimeProvider struct {
	// PublishUnconfigured updates the shared observation cache after reset commit.
	PublishUnconfigured func(uint64)
	Generation          uint64
	Mode                string
	loadDeployment      func(context.Context) (*RuntimeProvider, error)
	prepareDeployment   RuntimeDeploymentPreparer
	ProviderKind        string
	CoreURL             string
	SandboxLink         string
	InstallationID      string
	BackendFingerprint  string
	Provider            sandbox.SandboxProvider
	Suspension          *RuntimeSuspensionPolicy
}

type runtimeLifecycle struct {
	sessions         sessions.Reader
	sessionExecution *sessions.ExecutionOperations
	// deployment runs the allocation mutations on the lease; deployments and
	// reader run the pooled activity use case and reads.
	deployment      *deployment.ExecutionOperations
	deployments     *deployment.Service
	reader          deployment.Reader
	lease           Ownership
	registry        *runtimegateway.Registry
	links           *relay.Relay
	config          RuntimeProvider
	nodeID          string
	gate            chan struct{}
	ctx             context.Context
	stop            context.CancelFunc
	cancelMu        sync.Mutex
	reconcileCancel context.CancelFunc
	cursor          string
	pendingCursor   string
	connections     map[string]*runtimeConnection
	wakeHints       chan struct{}
}

func newRuntimeManager(owner Owner, deployments *deployment.Service, deploymentReader deployment.Reader, sessionReader sessions.Reader, registry *runtimegateway.Registry, links *relay.Relay, config *RuntimeProvider) (*runtimeManager, error) {
	if config == nil {
		return nil, nil
	}
	id, err := uuid.Parse(config.InstallationID)
	if err != nil || id == uuid.Nil || id.String() != config.InstallationID || config.loadDeployment == nil || config.prepareDeployment == nil || registry == nil {
		return nil, sandbox.ErrInvalid
	}
	ctx, stop := context.WithCancel(context.Background())
	return &runtimeManager{sessions: sessionReader, sessionExecution: owner.Sessions, deployment: owner.Deployment, deploymentService: deployments, deploymentReader: deploymentReader, lease: owner.Lease, registry: registry, links: links, setupInstallationID: config.InstallationID, loadDeployment: config.loadDeployment, prepareDeployment: config.prepareDeployment, publishUnconfigured: config.PublishUnconfigured, setupGate: make(chan struct{}, 1), mutationGate: make(chan struct{}, 1), ctx: ctx, cancel: stop, nodes: make(map[string]*runtimeNode), failed: make(chan error, 1), inventory: make(chan struct{}, 1)}, nil
}

func validatedRuntimeProvider(config *RuntimeProvider, registry *runtimegateway.Registry) (RuntimeProvider, error) {
	u, err := url.Parse(config.CoreURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || config.Provider == nil || registry == nil {
		return RuntimeProvider{}, sandbox.ErrInvalid
	}
	id, err := uuid.Parse(config.InstallationID)
	fingerprint, fingerprintErr := hex.DecodeString(config.BackendFingerprint)
	if err != nil || id == uuid.Nil || id.String() != config.InstallationID || fingerprintErr != nil || len(fingerprint) != 32 || hex.EncodeToString(fingerprint) != config.BackendFingerprint {
		return RuntimeProvider{}, sandbox.ErrInvalid
	}
	if err := sandbox.ValidateProvider(config.Provider); err != nil {
		return RuntimeProvider{}, err
	}
	copied := *config
	if copied.ProviderKind == "" || (copied.Mode != "nodes" && copied.Mode != "direct") {
		return RuntimeProvider{}, sandbox.ErrInvalid
	}
	if copied.Mode == "direct" && copied.Suspension != nil {
		return RuntimeProvider{}, sandbox.ErrInvalid
	}
	if config.Suspension != nil {
		policy := *config.Suspension
		if !sandbox.SupportsCheckpoint(config.Provider) || policy.IdleTimeout < time.Second || policy.Retention < time.Second {
			return RuntimeProvider{}, sandbox.ErrInvalid
		}
		copied.Suspension = &policy
	}
	return copied, nil
}

func (r *runtimeLifecycle) lock(ctx context.Context) error {
	select {
	case r.gate <- struct{}{}:
		if err := r.ctx.Err(); err != nil {
			<-r.gate
			return err
		}
		if err := ctx.Err(); err != nil {
			<-r.gate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-r.ctx.Done():
		return r.ctx.Err()
	}
}

// ProvisionEnvironment is an internal bootstrap operation for an already
// authorized hosted Environment. It does not enable public hosted admission.
func (w *Worker) ProvisionEnvironment(ctx context.Context, tenant, environment, providerKey string) (deployment.Allocation, error) {
	if w.runtimes == nil {
		return deployment.Allocation{}, ErrExecutionUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ctx, finish, err := w.runtimes.enter(ctx)
	if err != nil {
		return deployment.Allocation{}, err
	}
	defer finish()
	ready, err := w.runtimes.ensureDeployment(ctx)
	if err != nil {
		return deployment.Allocation{}, err
	}
	if !ready {
		return deployment.Allocation{}, ErrExecutionUnavailable
	}
	nodeID, err := w.runtimes.deploymentService.LifecycleNode(ctx, tenant, environment)
	if err != nil {
		return deployment.Allocation{}, err
	}
	node, err := w.runtimes.node(nodeID)
	if err != nil {
		return deployment.Allocation{}, err
	}
	r := node.lifecycle
	if err := r.lock(ctx); err != nil {
		return deployment.Allocation{}, err
	}
	defer func() { <-r.gate }()
	return r.provision(ctx, tenant, environment, providerKey)
}

// provision runs under the lifecycle gate and uses the durable one-shot receipt.
func (r *runtimeLifecycle) provision(ctx context.Context, tenant, environment, providerKey string) (deployment.Allocation, error) {
	provider := r.config.Provider
	if providerKey != r.config.InstallationID {
		return deployment.Allocation{}, sandbox.ErrInvalid
	}
	if r.config.SandboxLink == "" {
		return deployment.Allocation{}, deployment.ErrNoLink
	}
	environmentValue, err := r.sessions.GetEnvironment(ctx, tenant, environment)
	if err != nil {
		return deployment.Allocation{}, err
	}
	placement, err := parseEnvironmentPlacement(environmentValue.Configuration)
	if err != nil || placement.Type != "openai_hosted" {
		return deployment.Allocation{}, sandbox.ErrInvalid
	}
	key := deployment.AllocationKey{TenantID: tenant, EnvironmentID: environment}
	secret := make([]byte, 64)
	if _, err := rand.Read(secret); err != nil {
		return deployment.Allocation{}, err
	}
	// The device and Serve credentials; only their digests are stored.
	token, serve := hex.EncodeToString(secret[:32]), hex.EncodeToString(secret[32:])
	owner, err := r.deployment.ReserveAllocation(ctx, key, providerKey, runtimedevice.HashCredential(token), runtimedevice.HashCredential(serve))
	if err != nil {
		return owner, err
	}
	if owner.NodeID != r.nodeID {
		return owner, sandbox.ErrOwnership
	}
	if owner.Replayed {
		return owner, nil
	}
	if err := r.lease.CheckOwnership(ctx); err != nil {
		return owner, err
	}
	bootstrap := sandbox.Bootstrap{
		Reference: runtimeReference(owner), SessionID: owner.SessionID, DeviceID: owner.DeviceID,
		CoreURL: r.config.CoreURL, Credential: token, NetworkAccess: placement.NetworkAccess, AllowedDomains: placement.AllowedDomains,
		SandboxIO: sandboxbootstrap.Input{Version: sandboxbootstrap.Version, LinkURL: r.config.SandboxLink, Credential: serve, Resource: serveResource(owner)},
	}
	// An invalid input creates nothing: release the allocation as settled absent.
	info := sandbox.Info{Reference: bootstrap.Reference, State: "absent", CreateSettled: true}
	if err = bootstrap.Validate(); err == nil {
		info, err = provider.Create(ctx, bootstrap)
	}
	if info.Reference == runtimeReference(owner) && info.CreateSettled && info.State == "absent" {
		record, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		released, releaseErr := r.deployment.ReleaseAbsentCreation(record, owner)
		cancel()
		if releaseErr != nil {
			return owner, releaseErr
		}
		if err == nil {
			err = sandbox.ErrComputeUnconfirmed
		}
		return released, err
	}
	if info.Reference == runtimeReference(owner) && info.CreateSettled {
		settled, settleErr := r.deployment.SettleCreation(ctx, owner)
		if settleErr != nil {
			return owner, settleErr
		}
		owner = settled
	}
	if err != nil {
		// Explicit invalid/foreign bootstrap cannot become an authorized Runtime.
		// Other failures may hide a successful Create; retain recovery for those.
		if errors.Is(err, sandbox.ErrInvalid) || errors.Is(err, sandbox.ErrOwnership) {
			if _, cleanupErr := r.requestCleanup(ctx, owner); cleanupErr != nil {
				return owner, cleanupErr
			}
		}
		// Reconciliation observes the original allocation; it never sends Create again.
		return owner, err
	}
	if info.Reference != runtimeReference(owner) || info.ProviderID == "" || info.State != "running" || !info.BootstrapComplete {
		return owner, sandbox.ErrOwnership
	}
	return r.deployment.ObserveRunning(ctx, owner)
}

// ReconcileManagedRuntimes is also callable before serving admission. One scan
// observes existing allocations and bootstraps committed resources without an
// allocation. It never retries an existing Create or native work.
func (w *Worker) ReconcileManagedRuntimes(ctx context.Context) error {
	if w.runtimes == nil {
		return nil
	}
	return w.runtimes.reconcile(ctx)
}

func (r *runtimeLifecycle) reconcile(ctx context.Context) error {
	ctx, finish, err := r.beginReconcile(ctx)
	if err != nil {
		return err
	}
	defer finish()
	rows, err := r.reader.LifecycleAllocations(ctx, r.nodeID, r.cursor)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		wrapped := r.cursor != ""
		r.cursor = ""
		if wrapped {
			// Service the next page now instead of spending a ticker interval on EOF.
			// Refill only once so an empty store still returns without spinning.
			rows, err = r.reader.LifecycleAllocations(ctx, r.nodeID, "")
			if err != nil {
				return err
			}
		}
	}
	for _, owner := range rows {
		r.cursor = owner.ID
		operation, stop := context.WithTimeout(ctx, 30*time.Second)
		err := r.observe(operation, owner)
		r.recordObservation(ctx, owner, err)
		stop()
		if err != nil {
			if ownership := r.lease.CheckOwnership(ctx); ownership != nil {
				return ownership
			}
			// Provider errors can include operator configuration. Log safe identity
			// only; retain the durable owner for the next bounded observation.
			log.Ctx(ctx).Warn("managed Runtime observation incomplete", "allocation_id", owner.ID)
		}
	}
	return r.provisionPending(ctx)
}

func (r *runtimeLifecycle) observe(ctx context.Context, owner deployment.Allocation) error {
	if owner.ProviderKey != r.config.InstallationID || owner.NodeID != r.nodeID {
		return sandbox.ErrOwnership
	}
	if owner.SessionDeleted || owner.Expired || owner.State == "cleanup_pending" {
		var err error
		owner, err = r.requestCleanup(ctx, owner)
		if err != nil {
			return err
		}
		if peer, err := r.registry.LookupDevice(owner.DeviceID); err == nil {
			draining, err := peer.DrainArchivedCancellation(ctx)
			if err != nil {
				return err
			}
			if draining {
				return nil
			}
		}
		r.clearRuntimeState(owner)
	} else if owner.ComputePhase == "disabled" || owner.ComputePhase == "running" {
		if err := r.observeConnection(ctx, owner); err != nil {
			return err
		}
	}
	if owner.ComputePhase != "disabled" {
		return r.observeCompute(ctx, owner)
	}
	provider := r.config.Provider
	if owner.ProviderKey != r.config.InstallationID {
		return sandbox.ErrInvalid
	}
	if err := r.lease.CheckOwnership(ctx); err != nil {
		return err
	}
	info, err := provider.GetInfo(ctx, runtimeReference(owner))
	cleanup := owner.SessionDeleted || owner.Expired || owner.State == "cleanup_pending"
	// Resource drift disqualifies execution, not cleanup already authorized by
	// Core. Kill independently verifies ownership before removing anything.
	invalidCleanup := cleanup && errors.Is(err, sandbox.ErrInvalid) && !errors.Is(err, sandbox.ErrOwnership)
	if err != nil && !errors.Is(err, sandbox.ErrNotFound) && !invalidCleanup {
		return err
	}
	running := err == nil && info.Reference == runtimeReference(owner) && info.ProviderID != "" && info.State == "running"
	if (err == nil || invalidCleanup && info.CreateSettled) && info.Reference != runtimeReference(owner) {
		return sandbox.ErrOwnership
	}
	settled := info.CreateSettled && (err == nil || invalidCleanup)
	if (settled || running && info.BootstrapComplete) && !owner.CreateSettled {
		// Explicit settlement can accompany a configuration rejection. Missing
		// compute, a timeout or a successful Kill alone cannot settle Create.
		owner, err = r.deployment.SettleCreation(ctx, owner)
		if err != nil {
			return err
		}
	}
	if owner.SessionDeleted || owner.Expired || owner.State == "cleanup_pending" {
		owner, err = r.requestCleanup(ctx, owner)
		if err != nil {
			return err
		}
		if err := r.lease.CheckOwnership(ctx); err != nil {
			return err
		}
		if err := provider.Kill(ctx, runtimeReference(owner)); err != nil {
			return err
		}
		if !owner.CreateSettled {
			return nil // Keep scanning unknown creation; absence is not a final receipt.
		}
		_, err = r.deployment.ReleaseAllocation(ctx, owner)
		return err
	}
	// A stopped or missing container does not authorize destroying retained
	// workspace/history. Preserve it until explicit cleanup or actual expiry.
	if !running || !info.BootstrapComplete {
		if owner.NodeID != "" {
			if errors.Is(err, sandbox.ErrNotFound) && owner.CreateSettled {
				return sandbox.ErrNotFound
			}
			return sandbox.ErrComputeUnconfirmed
		}
		return nil
	}
	owner, err = r.deployment.ObserveRunning(ctx, owner)
	if err != nil {
		return err
	}
	environment, err := r.sessions.GetEnvironment(ctx, owner.TenantID, owner.EnvironmentID)
	if err != nil {
		return err
	}
	if r.config.Suspension != nil && environment.Initialization == "complete" {
		return r.enableCompute(ctx, owner)
	}
	if peer, err := r.registry.LookupDevice(owner.DeviceID); err != nil || peer.IsClosed() {
		return nil
	}
	renewed, err := provider.Renew(ctx, runtimeReference(owner))
	if err != nil {
		return err
	}
	if renewed.Reference != runtimeReference(owner) || renewed.State != "running" || !renewed.BootstrapComplete {
		return sandbox.ErrOwnership
	}
	_, err = r.deployment.KeepAllocation(ctx, owner)
	return err
}

// requestCleanup records the allocation's cleanup, which withdraws its Serve
// authority, then revokes its Link resource at the relay. The caller destroys
// the compute afterwards.
func (r *runtimeLifecycle) requestCleanup(ctx context.Context, owner deployment.Allocation) (deployment.Allocation, error) {
	pending, err := r.deployment.RequestCleanup(ctx, owner)
	if err != nil {
		return pending, err
	}
	r.links.RevokeResource(serveResource(pending).Ref())
	return pending, nil
}

// serveResource is the allocation's Link resource.
func serveResource(owner deployment.Allocation) sandboxbootstrap.Resource {
	return sandboxbootstrap.Resource{TenantID: owner.TenantID, EnvironmentID: owner.EnvironmentID, Kind: "allocation", ID: owner.ID, Generation: owner.ServeGeneration}
}

// Environment identity owns connectivity; preparation has an independent owner.
func (r *runtimeLifecycle) clearRuntimeState(owner deployment.Allocation) {
	delete(r.connections, owner.EnvironmentID)
}

func runtimeReference(owner deployment.Allocation) sandbox.Reference {
	return sandbox.Reference{TenantID: owner.TenantID, EnvironmentID: owner.EnvironmentID, AllocationID: owner.ID}
}

func (w *Worker) runManagedRuntimes(ctx context.Context) error {
	return w.runtimes.run(ctx)
}
