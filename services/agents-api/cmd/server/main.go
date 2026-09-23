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
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtime"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeenrollment"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory"
	historystoreresolver "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimehistory/storeresolver"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs"
	observationstoreresolver "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtimeobs/storeresolver"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/node"
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
	databaseURL, keysFile := os.Getenv("AGENTS_API_DATABASE_URL"), os.Getenv("AGENTS_API_KEYS_FILE")
	if databaseURL == "" || keysFile == "" {
		return errors.New("AGENTS_API_DATABASE_URL and AGENTS_API_KEYS_FILE are required")
	}
	content, err := os.ReadFile(keysFile)
	if err != nil {
		return errors.New("cannot read AGENTS_API_KEYS_FILE")
	}
	var keys []api.APIKey
	if err := json.Unmarshal(content, &keys); err != nil {
		return errors.New("AGENTS_API_KEYS_FILE must contain an array of API key bindings")
	}
	auth, err := api.NewAuthenticator(keys)
	if err != nil {
		return err
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
	transientOptions, modelProviderEndpoints, err := executionOptionsConfiguration()
	if err != nil {
		return err
	}
	oauthClient, err := oauthRefreshClient()
	if err != nil {
		return err
	}
	executionStore := store.NewWithCredentialCipherAndOAuthRefresh(pool, credentialKey, oauthClient)
	if err := executionStore.EnsureProjectScopes(ready, auth.ProjectScopes()); err != nil {
		return err
	}
	var workerDone chan error
	var worker *execution.Worker
	managedNodes, err := configureManagedNodes(executionStore, func(ctx context.Context) error {
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
	history, err := runtimeHistory(ctx, executionStore, os.Getenv("AGENTS_API_DAEMON_WS_URL") != "")
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
		runHistoryCleanup(cleanupCtx, history.Prune)
	}()
	defer func() { cancelCleanup(); <-cleanupDone }()
	options := []api.Option{api.WithSubagents(executionStore), api.WithSkills(executionStore), api.WithSourceFiles(executionStore), api.WithSessionArtifacts(executionStore), api.WithRuntimeObservations(observationService)}
	if managedNodes != nil {
		options = append(options, api.WithSandboxManager(executionStore, managedNodes.admin))
	}
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
	if wsURL := os.Getenv("AGENTS_API_DAEMON_WS_URL"); wsURL != "" {
		if managedNodes != nil && managedNodes.setup != nil {
			daemonHandler, registry, err = runtime.NewGatewayWithURLResolver(executionStore, wsURL, managedNodes.setup.webSocketURL(wsURL))
		} else {
			daemonHandler, registry, err = runtime.NewGateway(executionStore, wsURL)
		}
		if err != nil {
			return err
		}
		defer runtime.CloseConnections(registry)
		options = append(options, api.WithEnvironmentRemoteURL(wsURL))
	}
	if registry != nil {
		dispatcher := &execution.Dispatcher{Store: executionStore, Registry: registry,
			ManagedRuntimes: managed, Options: transientOptions}

		worker, err = execution.StartWorker(ctx, dispatcher)
		if err != nil {
			return err
		}
		if managedNodes != nil && managedNodes.setup != nil {
			options = append(options, api.WithSandboxDeploymentSetup(worker.InitializeSandboxDeployment))
		}
		workerDone = make(chan error, 1)
		go func() { workerDone <- worker.Run(ctx) }()
		defer func() {
			stop()
			if workerDone != nil {
				<-workerDone
			}
		}()
		options = append(options, api.WithExecution(worker), api.WithEnvironmentDirectoryReader(worker), api.WithEnvironmentFileWriter(worker))
		options = append(options, api.WithHarnesses(kinds))
		if managed != nil {
			options = append(options, api.WithHostedEnvironments())
		}
	}
	if history.SampleInterval > 0 {
		if worker == nil {
			return errors.New("Runtime history periodic sampling requires the execution worker")
		}
		sampler, err := runtimeobs.NewSampler(observationResolver, observationService, worker, runtimeobs.SamplerOptions{
			Interval: history.SampleInterval,
			Report: func(result runtimeobs.SweepResult) {
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
		go func() { samplerDone <- sampler.Run(samplerCtx) }()
		defer func() {
			cancelSampler()
			<-samplerDone
		}()
	}
	startupManaged := managed
	if managedNodes != nil && managedNodes.setup != nil {
		startupManaged = managedNodes.setup.selected.Load()
	}
	options = append(options, api.WithStartupConfiguration(coreStartupConfiguration(engine, kinds, registry != nil, modelProviderEndpoints, managedRuntimeProviderKind(startupManaged), startupManaged)))
	handler, err := api.NewHandler(executionStore, auth, engine, options...)
	if err != nil {
		return err
	}
	if daemonHandler != nil {
		mux := http.NewServeMux()
		mux.Handle("/api/v1/agent-daemon/", daemonHandler)
		mux.Handle("/api/v1/agent-daemon/enroll", runtimeenrollment.EnrollmentHandler(executionStore))

		if managedNodes != nil {
			mux.Handle("/core/v1/sandbox/node/connect", managedNodes.hub)
		}
		mux.Handle("/", handler)
		handler = mux
	}
	addr := serverAddress()
	server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	var localDone chan error
	if managedNodes != nil && managedNodes.local != nil {
		localDone = make(chan error, 1)
		go func() { localDone <- node.Run(ctx, *managedNodes.local) }()
		defer func() {
			stop()
			if localDone != nil {
				<-localDone
			}
		}()
	}
	select {
	case err := <-done:
		return err
	case err := <-localDone:
		localDone = nil
		stop()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
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
