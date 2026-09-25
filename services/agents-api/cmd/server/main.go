// Package main runs the standalone Agents API service.
//
// @title Agents API
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

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/coremetrics"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/databaseurl"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtime"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeenrollment"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory"
	historystoreresolver "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory/storeresolver"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	observationstoreresolver "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs/storeresolver"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		log.Bg().Error("agents-api startup failed", "error", err)
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
		return errors.New("AGENTS_API_DATABASE_URL is required")
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
	engine := os.Getenv("AGENTS_API_ENGINE")
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
	executionStore := store.NewWithCredentialCipherAndOAuthRefresh(pool, credentialKey, oauthClient)
	executionStore.SetPublicURL(public)
	installation, err := installationFacts(public)
	if err != nil {
		return err
	}
	metricsSource := &coreMetricsSource{store: executionStore, pool: pool}
	metrics := coremetrics.New(processStartedAt, buildRevision, metricsSource)
	auth, err := api.NewDatabaseAuthenticator(executionStore)
	if err != nil {
		return err
	}
	auditRetention, err := writeAuditRetention()
	if err != nil {
		return err
	}
	auditCleanupCtx, cancelAuditCleanup := context.WithCancel(ctx)
	auditCleanupDone := make(chan struct{})
	go func() {
		defer close(auditCleanupDone)
		runWriteAuditCleanup(auditCleanupCtx, executionStore, auditRetention, metrics)
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
	if managedNodes != nil {
		managed = managedNodes.runtime
	}
	observationSources := map[string]runtimeobs.Source{}
	if managedNodes != nil && managedNodes.setup != nil {
		observationSources[managed.InstallationID] = managedNodes.setup
	} else if managed != nil {
		if source, ok := managed.Provider.(runtimeobs.Source); ok {
			observationSources[managed.InstallationID] = source
		}
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
	options := []api.Option{api.WithCoreMetrics(metrics), api.WithSubagents(executionStore), api.WithSkills(executionStore), api.WithSourceFiles(executionStore), api.WithSessionArtifacts(executionStore), api.WithRuntimeObservations(observationService)}
	if managedNodes != nil {
		options = append(options, api.WithSandboxManager(executionStore, managedNodes.admin))
	}
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
	options = append(options, api.WithProjectAPIKeys(executionStore, keyAdmin), api.WithWriteAudit(executionStore, keyAdmin), api.WithAdminManagement(executionStore),
		api.WithInstallation(installation, executionStore.AddressBindings))
	if history.Reader != nil {
		historyResolver, resolverErr := historystoreresolver.NewResolver(executionStore)
		if resolverErr != nil {
			return resolverErr
		}
		historyService, serviceErr := runtimehistory.NewService(historyResolver, history.Reader)
		if serviceErr != nil {
			return serviceErr
		}
		options = append(options, api.WithRuntimeHistory(historyService))
	}
	var daemonHandler http.Handler
	var registry *gateway.Registry
	if public != "" {
		wsURL, err := runtimeWebSocketURL(public)
		if err != nil {
			return err
		}
		daemonHandler, registry, err = runtime.NewGatewayWithURLResolver(executionStore, wsURL, managedNodes.webSocketURL(wsURL))
		if err != nil {
			return err
		}
		defer runtime.CloseConnections(registry)
		options = append(options, api.WithEnvironmentRemoteURL(wsURL))
	}
	if registry != nil {
		dispatcher := &execution.Dispatcher{Store: executionStore, Registry: registry,
			ManagedRuntimes: managed, MaxConcurrentExecutions: concurrency}

		worker, err = execution.StartWorker(ctx, dispatcher)
		if err != nil {
			return err
		}
		if managedNodes != nil && managedNodes.setup != nil {
			options = append(options, api.WithSandboxDeploymentSetup(worker.InitializeSandboxDeployment), api.WithSandboxDeploymentChanges(worker.UpdateSandboxDeployment, worker.SetSandboxMaintenance))
		}
		workerDone = make(chan error, 1)
		go func() { workerDone <- worker.Run(ctx) }()
		defer func() {
			stop()
			if workerDone != nil {
				<-workerDone
			}
		}()
		options = append(options, api.WithExecution(worker), api.WithSessionArchive(worker.ArchiveManagedSession), api.WithEnvironmentDirectoryReader(worker), api.WithEnvironmentFileWriter(worker))
		options = append(options, api.WithHarnesses(kinds), api.WithModelProviderDefaults(executionStore.DeploymentModelProvider))
		if managed != nil {
			options = append(options, api.WithHostedEnvironments())
		}
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
	handler, err := api.NewHandler(executionStore, auth, engine, options...)
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
