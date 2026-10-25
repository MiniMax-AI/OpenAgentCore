package execution

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/internal/agentcapabilities"
	"github.com/MiniMax-AI/OpenAgentCore/internal/agentdaemon/proto"
	"github.com/MiniMax-AI/OpenAgentCore/internal/obs/log"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/environmentconfig"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/runtimegateway"
	"github.com/MiniMax-AI/OpenAgentCore/services/core/internal/sessions"
)

// The leased Worker owns preparation for every Environment. Compute managers
// never run initialization. Bounded concurrent owners prevent one slow machine
// from blocking other environments or resource reclamation.
func (w *Worker) runEnvironmentInitializations(ctx context.Context) error {
	active := make(map[string]bool)
	done := make(chan string, w.executionConcurrency())
	var running sync.WaitGroup
	ctx, stop := context.WithCancel(ctx)
	defer func() { stop(); running.Wait() }()
	cursor := ""
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case id := <-done:
			delete(active, id)
		case <-w.dispatcher.Registry.Announced():
		case <-ticker.C:
		}
		rows, err := w.dispatcher.SessionsReader.ListEnvironmentInitializations(ctx, cursor)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			cursor = ""
		}
		for _, owner := range rows {
			cursor = owner.EnvironmentID
			if active[owner.EnvironmentID] {
				continue
			}
			if owner.State == "running" {
				// Lost process-local progress cannot prove which side effects ran.
				if err := w.dispatcher.sessionExecution.FailEnvironmentInitialization(ctx, owner, sessions.ProvisioningFailure{Category: sessions.ProvisioningCoreRestart}); err != nil && !errors.Is(err, sessions.ErrNotFound) {
					return err
				}
				continue
			}
			if len(active) >= w.executionConcurrency() {
				continue
			}
			peer, err := w.dispatcher.authorizedPeer(ctx, owner.DeviceID)
			if err != nil {
				if errors.Is(err, sessions.ErrNotFound) || errors.Is(err, runtimegateway.ErrSessionClosed) || errors.Is(err, runtimegateway.ErrDeviceNotRegistered) {
					continue
				}
				return err
			}
			harness, found, known := peer.AgentKindStatus(owner.Engine)
			if !known {
				continue
			}
			if !found || !harness.Available {
				if err := w.dispatcher.sessionExecution.FailEnvironmentInitialization(ctx, owner, sessions.ProvisioningFailure{Step: sessions.ProvisioningHarness}); err != nil && !errors.Is(err, sessions.ErrNotFound) {
					return err
				}
				continue
			}
			if err := w.dispatcher.sessionExecution.ClaimEnvironmentInitialization(ctx, owner); err != nil {
				if errors.Is(err, sessions.ErrNotFound) || errors.Is(err, sessions.ErrTurnConflict) {
					continue
				}
				return err
			}
			active[owner.EnvironmentID] = true
			running.Add(1)
			go func(owner sessions.EnvironmentInitialization) {
				defer running.Done()
				w.initializeEnvironment(ctx, owner)
				done <- owner.EnvironmentID
			}(owner)
		}
	}
}

func (w *Worker) initializeEnvironment(ctx context.Context, owner sessions.EnvironmentInitialization) {
	operation, cancel := context.WithTimeout(ctx, time.Duration(proto.RuntimePrepareMaxBudgetMS)*time.Millisecond)
	defer cancel()
	failure := sessions.ProvisioningFailure{}
	steps := []sessions.InitializationStep{}
	err := w.prepareEnvironment(operation, owner, &failure, &steps)
	// Steps stay recorded for a failed initialization too, but a write failure never fails
	// one that succeeded. Each step was already logged when it finished.
	recordSteps, stopSteps := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	if recordErr := w.dispatcher.sessionExecution.RecordEnvironmentInitializationSteps(recordSteps, owner, steps); recordErr != nil {
		log.Warn(ctx, "Environment initialization steps not recorded", "environment_id", owner.EnvironmentID, "session_id", owner.SessionID)
	}
	stopSteps()
	if err == nil {
		err = w.dispatcher.sessionExecution.CompleteEnvironmentInitialization(operation, owner)
		if err == nil {
			w.wakeScheduler()
		}
	}
	if err != nil {
		if failure.Step == "" && failure.Category == "" {
			failure.Category = provisioningCategory(err)
		}
		log.Warn(ctx, "Environment preparation failed", "environment_id", owner.EnvironmentID, "session_id", owner.SessionID, "step", failure.Step, "category", string(failure.Category))
		// A later scan settles an unrecorded failure; it never retries the setup.
		record, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		_ = w.dispatcher.sessionExecution.FailEnvironmentInitialization(record, owner, failure)
	}
}

func (w *Worker) prepareEnvironment(ctx context.Context, owner sessions.EnvironmentInitialization, failure *sessions.ProvisioningFailure, steps *[]sessions.InitializationStep) error {
	environment, err := w.dispatcher.SessionsReader.GetEnvironment(ctx, owner.TenantID, owner.EnvironmentID)
	if err != nil {
		return err
	}
	var cfg struct {
		Files []environmentconfig.InitialFileMetadata `json:"files"`
	}
	if json.Unmarshal(environment.Configuration, &cfg) != nil || len(cfg.Files) > 50 {
		return sessions.ErrInvalidInput
	}
	setup, err := w.dispatcher.SessionsReader.ReadEnvironmentSetup(ctx, owner.TenantID, owner.SessionID)
	if err != nil {
		return err
	}
	peer, err := w.dispatcher.authorizedPeer(ctx, owner.DeviceID)
	if err != nil {
		return err
	}
	placement, err := parseEnvironmentPlacement(environment.Configuration)
	if err != nil {
		return err
	}
	if placement.Type == "openai_hosted" {
		info, found, known := peer.AgentKindStatus(owner.Engine)
		if !known || !found || !info.Available || w.dispatcher.validateEnvironmentHistory(owner.Engine, environment, info.Capabilities.RetainedNativeHistory.IsSupported()) != nil {
			*failure = sessions.ProvisioningFailure{Step: sessions.ProvisioningHarness}
			return sessions.ErrInvalidInput
		}
	}
	identity := agentcapabilities.Identity{EnvironmentID: owner.EnvironmentID, SessionID: owner.SessionID}
	operations := setupOperations(setup)
	step := func(index int) error {
		err := w.lease.CheckOwnership(ctx)
		if err != nil {
			err = errors.Join(errOwnershipLost, err)
		}
		if err == nil {
			var currentPeer = peer
			currentPeer, err = w.dispatcher.authorizedPeer(ctx, owner.DeviceID)
			if err == nil && currentPeer != peer {
				err = errRuntimeConnectionChanged
			}
		}
		candidate := sessions.ProvisioningFailure{Step: sessions.ProvisioningInitialFile}
		var result proto.RuntimePrepareResultPayload
		var recorded sessions.InitializationStep
		if err == nil && index < len(cfg.Files) {
			var metadata environmentconfig.InitialFileMetadata
			var body []byte
			metadata, body, err = w.dispatcher.SessionsReader.ReadInitialEnvironmentFile(ctx, owner.TenantID, owner.SessionID, index)
			if err == nil {
				result, err = installInitialFile(ctx, peer, identity, metadata, body)
				recorded, _ = runtimeSetupOperation{Request: proto.RuntimePreparePayload{Action: "file"}}.step(result)
			}
		} else if err == nil {
			command := operations[index-len(cfg.Files)]
			candidate = command.provisioningFailure(0)
			result, err = runRuntimeSetup(ctx, peer, identity, command)
			recorded, _ = command.step(result)
		}
		if recorded.Step != "" {
			*steps = append(*steps, recorded)
			attrs := []any{"environment_id", owner.EnvironmentID, "session_id", owner.SessionID, "step", recorded.Step, "duration_ms", recorded.DurationMS}
			if recorded.Index != nil {
				attrs = append(attrs, "index", *recorded.Index)
			}
			log.With(attrs...).InfoContext(ctx, "Environment initialization step")
		}
		var confirmed *runtimeStepFailure
		if errors.As(err, &confirmed) {
			candidate.ExitCode = confirmed.exitCode
			*failure = candidate
		}
		return err
	}
	total := len(cfg.Files) + len(operations)
	cache, err := w.preparationCache(ctx, owner, len(cfg.Files), operations)
	if err != nil {
		return err
	}
	return cache.run(ctx, func() error {
		for index := 0; index < total; index++ {
			if err := step(index); err != nil {
				return err
			}
		}
		return nil
	}, func() error {
		// Only finalize binds the restored capability installation to this Session.
		if len(operations) > 0 && operations[len(operations)-1].Request.Action == "finalize" {
			return step(total - 1)
		}
		return nil
	})
}
