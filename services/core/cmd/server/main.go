// Package main runs the standalone Agents API service.
//
// @title OpenAgentCore Agents API
// @version 1
// @description Supported single-Agent execution resources from the pinned openai-python beta/agents contract. Bearer keys bind an execution principal to one project; optional OpenAI-Organization and OpenAI-Project headers must match that binding.
// @license.name Apache 2.0
// @license.url https://www.apache.org/licenses/LICENSE-2.0.html
// @BasePath /v1
// @schemes http https
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @securityDefinitions.apikey DeploymentAdminAuth
// @in header
// @name Authorization
// @securityDefinitions.apikey NodeEnrollmentAuth
// @in header
// @name Authorization
// @securityDefinitions.apikey NodeAuth
// @in header
// @name Authorization
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/databaseurl"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmenttemplates"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/nativeinstaller"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/agentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/filepg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/templatepg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/vaultpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtime"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeenrollment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimehistory"
	historystoreresolver "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimehistory/storeresolver"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	observationstoreresolver "github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs/storeresolver"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/store"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		log.Bg().Error("oac-core startup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	log.Init(log.ConfigFromEnv())
	if err := validateProcessConfiguration(); err != nil {
		return err
	}
	public, err := publicURL()
	if err != nil {
		return err
	}
	concurrency, err := executionConcurrency()
	if err != nil {
		return err
	}
	logConfigurationSources()
	databaseURL, err := databaseurl.FromEnvironment()
	if err != nil {
		return err
	}
	if databaseURL == "" {
		return errors.New("OAC_DATABASE_URL is required")
	}
	credentialKey, err := credentialCipher()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return errors.New("invalid Agents API database configuration")
	}
	defer pool.Close()
	ready, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(ready); err != nil {
		return errors.New("Agents API database connection failed")
	}
	engine := os.Getenv("OAC_DEFAULT_HARNESS")
	if engine == "" {
		engine = "codex"
	}
	kinds, err := enabledHarnesses(engine)
	if err != nil {
		return err
	}
	oauthClient, err := oauthRefreshClient()
	if err != nil {
		return err
	}
	executionStore := store.NewWithCredentialCipher(pool, credentialKey)
	executionStore.SetPublicURL(public)
	units := pgunit.NewPool(pool)
	auditStore := auditpg.New(units)
	agentStore := agentpg.New(units, credentialKey)
	agentService, err := agents.NewService(agentStore)
	if err != nil {
		return err
	}
	vaultStore := vaultpg.New(units)
	vaultService, err := vaults.NewService(vaultStore, credentialKey, oauthClient)
	if err != nil {
		return err
	}
	templateStore := templatepg.New(units, credentialKey)
	environmentTemplates, err := environmenttemplates.NewService(templateStore)
	if err != nil {
		return err
	}
	installation, err := installationFacts(public)
	if err != nil {
		return err
	}
	metricsSource := &coreMetricsSource{store: executionStore, pool: pool}
	metrics := coremetrics.New(processStartedAt, buildRevision, metricsSource)
	auditRetention, err := writeAuditRetention()
	if err != nil {
		return err
	}
	auditCleanupCtx, cancelAuditCleanup := context.WithCancel(ctx)
	auditCleanupDone := make(chan struct{})
	go func() {
		defer close(auditCleanupDone)
		runWriteAuditCleanup(auditCleanupCtx, auditStore, auditRetention, metrics)
	}()
	defer func() { cancelAuditCleanup(); <-auditCleanupDone }()
	var workerDone chan error
	var worker *execution.Worker
	managedNodes, err := configureManagedNodes(executionStore, public, func(ctx context.Context) error {
		if worker == nil {
			return errors.New("sandbox execution owner is unavailable")
		}
		return worker.CheckOwnership(ctx)
	})
	if err != nil {
		return err
	}
	defer managedNodes.close()
	var managed *execution.RuntimeProvider
	observationSources := map[string]runtimeobs.SourceResolver{}
	if managedNodes != nil {
		managed = managedNodes.runtime
		observationSources[managed.InstallationID] = managedNodes.setup
	}
	observationResolver, err := observationstoreresolver.NewResolver(executionStore)
	if err != nil {
		return err
	}
	history, err := runtimeHistory(ctx, executionStore, public != "")
	if err != nil {
		return err
	}
	observationService, err := runtimeobs.NewService(observationResolver, observationSources, history.Options...)
	if err != nil {
		if history.Exporter != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			closeRuntimeHistory(closeCtx, history.Exporter)
		}
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = observationService.Close(closeCtx)
		if history.Exporter != nil {
			closeRuntimeHistory(closeCtx, history.Exporter)
		}
	}()
	cleanupCtx, cancelCleanup := context.WithCancel(ctx)
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		runHistoryCleanup(cleanupCtx, history.Prune, metrics)
	}()
	defer func() { cancelCleanup(); <-cleanupDone }()
	var keyAdmin *api.DeploymentAuthenticator
	if managedNodes != nil {
		keyAdmin = managedNodes.admin
	} else {
		keyAdmin, err = deploymentAdminAuthenticator()
		if err != nil {
			return err
		}
	}
	if err := api.ValidateCredentialSeparation(ctx, keyAdmin, executionStore); err != nil {
		return err
	}
	historyResolver, err := historystoreresolver.NewResolver(executionStore)
	if err != nil {
		return err
	}
	historyService, err := runtimehistory.NewService(historyResolver, history.Reader)
	if err != nil {
		return err
	}
	var daemonHandler http.Handler
	var registry *runtimegateway.Registry
	var executorURL string
	var nativeInstaller *api.NativeInstaller
	if public != "" {
		executorURL, err = runtimeWebSocketURL(public)
		if err != nil {
			return err
		}
		daemonHandler, registry, err = runtime.NewGateway(executionStore, executorURL)
		if err != nil {
			return err
		}
		defer runtime.CloseConnections(registry)
		var catalog *nativeinstaller.Catalog
		directory := os.Getenv("OAC_NATIVE_INSTALLER_DIR")
		if directory == "" {
			if _, err := os.Stat("/opt/oac/native-installers/catalog.json"); err == nil {
				directory = "/opt/oac/native-installers"
			}
		}
		if directory != "" {
			catalog, err = nativeinstaller.Load(directory, buildRevision)
			if err != nil {
				return err
			}
		}
		if buildRevision != "" {
			nativeInstaller = &api.NativeInstaller{Version: buildRevision, Catalog: catalog}
		}
	}
	if registry != nil {
		dispatcher := &execution.Dispatcher{Store: executionStore, Registry: registry, Credentials: vaultService,
			ManagedRuntimes: managed, MaxConcurrentExecutions: concurrency}
		lease, err := pgunit.AcquireLease(ctx, pool)
		if err != nil {
			return err
		}
		// From this call on the Worker closes the lease, even when it fails to start.
		worker, err = execution.StartWorker(ctx, dispatcher, execution.Owner{Lease: lease, Store: store.NewExecution(executionStore, lease)})
		if err != nil {
			return err
		}
		workerDone = make(chan error, 1)
		go func() { workerDone <- worker.Run(ctx) }()
		defer func() {
			stop()
			if workerDone != nil {
				<-workerDone
			}
		}()
	}
	if history.SampleInterval == 0 {
		metrics.StopJob("runtime_sampler")
	}
	if history.SampleInterval > 0 {
		if worker == nil {
			return errors.New("Runtime history periodic sampling requires the execution worker")
		}
		sampler, err := runtimeobs.NewSampler(observationResolver, observationService, worker, runtimeobs.SamplerOptions{
			Interval: history.SampleInterval,
			Report: func(result runtimeobs.SweepResult) {
				sampleCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				sampleErr := worker.CheckOwnership(sampleCtx)
				if sampleErr == nil {
					_, sampleErr = executionStore.SampleNodeHostHistory(sampleCtx)
				}
				cancel()
				if !result.Complete {
					sampleErr = errors.New("incomplete Runtime sampling sweep")
				}
				metrics.ReportJob("runtime_sampler", result.CompletedAt, metricPtr(int64(result.Observed)), metricPtr(int64(result.Failed)), sampleErr)
				fields := []any{"listed", result.Listed, "observed", result.Observed, "failed", result.Failed, "complete", result.Complete}
				if result.Complete {
					log.Bg().Debug("Runtime history sampling sweep complete", fields...)
				} else {
					log.Bg().Warn("Runtime history sampling sweep incomplete", fields...)
				}
			},
		})
		if err != nil {
			return err
		}
		samplerCtx, cancelSampler := context.WithCancel(ctx)
		samplerDone := make(chan error, 1)
		go func() { defer metrics.StopJob("runtime_sampler"); samplerDone <- sampler.Run(samplerCtx) }()
		defer func() {
			cancelSampler()
			<-samplerDone
		}()
	}
	metricsSource.worker, metricsSource.registry = worker, registry
	metricsCtx, cancelMetrics := context.WithCancel(ctx)
	metricsDone := make(chan struct{})
	go func() { defer close(metricsDone); metrics.Run(metricsCtx) }()
	defer func() { cancelMetrics(); <-metricsDone }()
	fileStore := filepg.New(units)
	fileService, err := files.NewService(fileStore)
	if err != nil {
		return err
	}
	deps := api.Dependencies{
		Engine: engine, Harnesses: kinds, CoreKeys: keyAdmin,
		Installation: installation, InstallationBindings: executionStore,
		Projects: executionStore, ModelProviders: executionStore,
		Vaults: vaultService, VaultsReader: vaultStore,
		Skills:               executionStore,
		EnvironmentTemplates: environmentTemplates, EnvironmentTemplatesReader: templateStore,
		Files: fileService, FilesReader: fileStore,
		Agents: agentService, AgentsReader: agentStore,
		Sessions: executionStore, SessionEvents: executionStore, SessionHistory: executionStore,
		Subagents: executionStore, Artifacts: executionStore, SessionAdmin: executionStore,
		Environments: executionStore, ExecutorConnections: executorConnections{store: executionStore, registry: registry},
		Admin: executionStore, AdminAudit: auditStore, WriteAudit: auditStore, Metrics: metrics,
		RuntimeObservations: observationService, RuntimeHistory: historyService,
	}
	if worker != nil {
		deps.Execution = &api.Execution{ExecutorURL: executorURL, Admission: worker, SessionArchive: worker, Workspaces: worker, NativeInstaller: nativeInstaller}
	}
	if managedNodes != nil {
		deps.Sandboxes = &api.Sandboxes{Deployment: executionStore, DeploymentChanges: worker, ConfigurationDiscovery: managedNodes.setup}
	}
	handler, err := api.NewHandler(deps)
	if err != nil {
		return err
	}
	if daemonHandler != nil {
		routes := daemonRoutes{gateway: daemonHandler,
			enrollment: runtimeenrollment.EnrollmentHandler(executionStore),
			connection: runtimeenrollment.ConnectionHandler(executionStore, registry)}
		if managedNodes != nil {
			routes.nodeConnect = managedNodes.hub
		}
		handler = serverHandler(handler, &routes)
	}
	addr := serverAddress()
	server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		return err
	case err := <-workerDone:
		workerDone = nil
		stop()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
