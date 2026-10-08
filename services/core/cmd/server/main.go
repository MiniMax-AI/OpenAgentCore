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
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/internal/sandboxlink/relay"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/agents"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/api"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/coremetrics"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/deployment/placement"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmenttemplates"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/execution"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/files"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/modelconfiguration"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/nativeinstaller"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/oauthrefresh"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/agentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/auditpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/coremetricspg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/deploymentpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/filepg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/modelconfigurationpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/pgunit"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/projectpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/sessionpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/skillpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/templatepg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/persistence/postgres/vaultpg"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/processconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/projects"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtime"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeenrollment"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimehistory"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimeobs"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sandbox/providers"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/skills"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/vaults"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	config, err := processconfig.Load()
	if len(os.Args) > 1 && os.Args[1] == "check-config" {
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		return
	}
	if err == nil {
		err = run(config)
	}
	if err != nil {
		log.Bg().Error("oac-core startup failed", "error", err)
		os.Exit(1)
	}
}

func run(config processconfig.Config) error {
	log.Init(config.Log)
	log.Bg().Info("Core process configuration loaded from the process environment")
	if file := config.RuntimeHistory.File; file != "" {
		log.Bg().Info("Core auxiliary configuration", "setting", "OAC_HISTORY_SETTINGS_FILE", "path", file)
	}
	credentialKey := config.CredentialKey
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	migrating, cancelMigration := context.WithTimeout(ctx, 2*time.Minute)
	err := migrations.Apply(migrating, config.DatabaseURL)
	cancelMigration()
	if err != nil {
		return fmt.Errorf("Agents API database migration failed: %w", err)
	}
	pool, err := pgxpool.New(ctx, config.DatabaseURL)
	if err != nil {
		return errors.New("invalid Agents API database configuration")
	}
	defer pool.Close()
	ready, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(ready); err != nil {
		return errors.New("Agents API database connection failed")
	}
	oauthClient, err := oauthrefresh.NewClient(config.OAuthTrustedOrigins)
	if err != nil {
		return err
	}
	units := pgunit.NewPool(pool)
	auditStore := auditpg.New(units)
	agentStore := agentpg.New(units, credentialKey)
	agentService, err := agents.NewService(agentStore)
	if err != nil {
		return err
	}
	vaultStore := vaultpg.New(units, credentialKey)
	vaultService, err := vaults.NewService(vaultStore, oauthClient)
	if err != nil {
		return err
	}
	templateStore := templatepg.New(units, credentialKey)
	environmentTemplates, err := environmenttemplates.NewService(templateStore)
	if err != nil {
		return err
	}
	modelConfigurationStore := modelconfigurationpg.New(units, credentialKey)
	modelConfigurationService, err := modelconfiguration.NewService(modelConfigurationStore)
	if err != nil {
		return err
	}
	skillStore := skillpg.New(units, credentialKey)
	skillService, err := skills.NewService(skillStore, skillStore)
	if err != nil {
		return err
	}
	projectStore := projectpg.New(units)
	projectService, err := projects.NewService(projectStore)
	if err != nil {
		return err
	}
	sandboxProviders := providers.Builtin()
	// The placement rules are built once: the provider declarations and the
	// public URL never change while Core runs.
	placementRules, err := placement.NewRules(sandboxProviders, config.PublicOrigin.String())
	if err != nil {
		return err
	}
	deploymentStore := deploymentpg.New(units, credentialKey)
	deploymentService, err := deployment.NewService(deploymentStore, deploymentStore, sandboxProviders, placementRules)
	if err != nil {
		return err
	}
	sessionStore := sessionpg.New(units, credentialKey)
	sessionService, err := sessions.NewService(sessionStore, placementRules)
	if err != nil {
		return err
	}
	// Node callbacks run only once the HTTP server serves, after the Worker starts.
	var worker *execution.Worker
	managedNodes := configureManagedNodes(deploymentService, deploymentStore, sandboxProviders, config, func(ctx context.Context) error {
		return worker.CheckOwnership(ctx)
	})
	defer managedNodes.hub.Close()
	observationSource := managedNodes.setup.observationSource
	observationResolver, err := deployment.NewObservationResolver(sessionStore, deploymentStore)
	if err != nil {
		return err
	}
	history, err := runtimeHistory(ctx, units, config.RuntimeHistory)
	if err != nil {
		return err
	}
	observationService, err := runtimeobs.NewService(observationResolver, observationSource, history.Options...)
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
	if err := api.ValidateCredentialSeparation(ctx, config.CoreKeys, projectStore); err != nil {
		return err
	}
	historyService, err := runtimehistory.NewService(sessionStore, history.Reader)
	if err != nil {
		return err
	}
	if err := sessionStore.RegisterAgentHost(ctx, config.AgentHostID, config.AgentHostCredentialHash); err != nil {
		return fmt.Errorf("agent host registration failed: %w", err)
	}
	executorURL := config.PublicOrigin.DaemonWebSocket()
	links := runtimegateway.NewLinkAuthority(sessionStore)
	daemonHandler, registry, err := runtime.NewGateway(sessionStore, sessionService, sessionStore, links, executorURL)
	if err != nil {
		return err
	}
	defer runtime.CloseConnections(registry)
	linkRelay := relay.New(links)
	defer linkRelay.Close()
	var catalog *nativeinstaller.Catalog
	if config.NativeInstallers != "" {
		catalog, err = nativeinstaller.Load(config.NativeInstallers, buildRevision)
		if err != nil {
			return err
		}
	}
	var nativeInstaller *api.NativeInstaller
	if buildRevision != "" {
		nativeInstaller = &api.NativeInstaller{Version: buildRevision, Base: config.PublicOrigin.InstallerBase(), Catalog: catalog}
	}
	dispatcher := &execution.Dispatcher{Registry: registry,
		Credentials: vaultService, Observer: modelConfigurationStore, Deployment: deploymentService, DeploymentReader: deploymentStore,
		Sessions:        sessionService,
		SessionsReader:  sessionStore,
		Links:           linkRelay,
		ManagedRuntimes: managedNodes.runtime, MaxConcurrentExecutions: config.ExecutionConcurrency}
	lease, err := pgunit.AcquireLease(ctx, pool)
	if err != nil {
		return err
	}
	deploymentExecution, err := deployment.NewExecutionOperations(deploymentService, deploymentpg.NewExecution(lease, credentialKey))
	if err != nil {
		return errors.Join(err, lease.Close(ctx))
	}
	sessionExecution, err := sessions.NewExecutionOperations(sessionpg.NewExecution(lease))
	if err != nil {
		return errors.Join(err, lease.Close(ctx))
	}
	// From this call on the Worker closes the lease, even when it fails to start.
	worker, err = execution.StartWorker(ctx, dispatcher, execution.Owner{
		Lease:      lease,
		Deployment: deploymentExecution,
		Sessions:   sessionExecution,
	})
	if err != nil {
		return err
	}
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(ctx) }()
	defer func() {
		stop()
		if workerDone != nil {
			<-workerDone
		}
	}()
	// The Worker owns every sampling sweep.
	sampler, err := runtimeobs.NewSampler(observationResolver, observationService, worker, runtimeobs.SamplerOptions{})
	if err != nil {
		return err
	}
	sampling := coremetrics.Periodic{ID: "runtime_sampler", Every: history.SampleInterval, Run: func(ctx context.Context) (*int64, int64, error) {
		result := sampler.Sweep(ctx)
		sampleCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := worker.CheckOwnership(sampleCtx)
		if err == nil {
			_, err = deploymentStore.SampleHostHistory(sampleCtx)
		}
		cancel()
		fields := []any{"listed", result.Listed, "observed", result.Observed, "failed", result.Failed, "complete", result.Complete}
		if !result.Complete {
			log.Bg().Warn("Runtime history sampling sweep incomplete", fields...)
			err = errors.New("incomplete Runtime sampling sweep")
		} else {
			log.Bg().Debug("Runtime history sampling sweep complete", fields...)
		}
		return metricPtr(int64(result.Observed)), int64(result.Failed), err
	}}
	metricsSource := &coreMetricsSource{store: coremetricspg.New(units), pool: pool, worker: worker, registry: registry}
	metrics, err := coremetrics.New(processStartedAt, buildRevision, metricsSource, sampling,
		prune("history_cleanup", 2*time.Second, history.Prune),
		prune("audit_cleanup", 5*time.Second, func(ctx context.Context) (int64, error) {
			return auditStore.DeleteExpiredWriteOperations(ctx, time.Now().Add(-config.WriteAuditRetention), 1000)
		}))
	if err != nil {
		return err
	}
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
		Engine: config.DefaultHarness, Harnesses: config.Harnesses, CoreKeys: config.CoreKeys,
		Installation: installationFacts(config), InstallationBindings: deploymentService,
		Projects: projectService, ProjectsReader: projectStore,
		ModelProviders: modelConfigurationService, ModelProvidersReader: modelConfigurationStore,
		Vaults: vaultService, VaultsReader: vaultStore,
		Skills: skillService, SkillsReader: skillStore,
		EnvironmentTemplates: environmentTemplates, EnvironmentTemplatesReader: templateStore,
		Files: fileService, FilesReader: fileStore,
		Agents: agentService, AgentsReader: agentStore,
		Sessions:        sessionService,
		SessionsReader:  sessionStore,
		SessionCreation: sessionService,
		SessionEvents:   sessionStore,
		Turns:           sessionStore,
		Items:           sessionStore,
		Subagents:       sessionStore,
		Artifacts:       sessionService,
		ArtifactsReader: sessionStore,
		SessionAdmin:    sessionStore,
		Environments:    sessionService, EnvironmentsReader: sessionStore, ExecutorConnections: executorConnections{sessions: sessionStore, registry: registry},
		Admin: sessionStore, AdminAudit: auditStore, WriteAudit: auditStore, Metrics: metrics,
		RuntimeObservations: observationService, RuntimeHistory: historyService,
		Execution: api.Execution{
			ExecutorURL:      executorURL,
			SessionAdmission: worker,
			InputAdmission:   worker,
			SessionArchive:   deploymentExecution,
			Workspaces:       worker,
			Links:            linkRelay,
			NativeInstaller:  nativeInstaller,
		},
		Sandboxes: api.Sandboxes{
			Deployment:             deploymentService,
			NodeAllocations:        deploymentStore,
			DeploymentChanges:      worker,
			DeploymentReset:        worker,
			ConfigurationDiscovery: managedNodes.setup,
		},
	}
	apiHandler, err := api.NewHandler(deps)
	if err != nil {
		return err
	}
	handler := serverHandler(apiHandler, &daemonRoutes{gateway: daemonHandler,
		enrollment:  runtimeenrollment.EnrollmentHandler(sessionService),
		connection:  runtimeenrollment.ConnectionHandler(sessionStore, registry),
		nodeConnect: managedNodes.hub})
	server := &http.Server{Addr: config.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
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
