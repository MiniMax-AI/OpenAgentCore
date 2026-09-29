package execution

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentcapabilities"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
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
		case <-ticker.C:
		}
		rows, err := w.dispatcher.Store.ListEnvironmentInitializations(ctx, cursor)
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
				if err := w.dispatcher.Store.FailEnvironmentInitialization(ctx, owner, store.ProvisioningFailure{}); err != nil && !errors.Is(err, store.ErrNotFound) {
					return err
				}
				continue
			}
			if len(active) >= w.executionConcurrency() {
				continue
			}
			peer, err := w.dispatcher.authorizedPeer(ctx, owner.DeviceID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) || errors.Is(err, gateway.ErrSessionClosed) || errors.Is(err, gateway.ErrDeviceNotRegistered) {
					continue
				}
				return err
			}
			harness, found, known := peer.AgentKindStatus(owner.Engine)
			if !known {
				continue
			}
			if !found || !harness.Available {
				if err := w.dispatcher.Store.FailEnvironmentInitialization(ctx, owner, store.ProvisioningFailure{Step: store.ProvisioningHarness}); err != nil && !errors.Is(err, store.ErrNotFound) {
					return err
				}
				continue
			}
			if err := w.dispatcher.Store.ClaimEnvironmentInitialization(ctx, owner); err != nil {
				if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrTurnConflict) {
					continue
				}
				return err
			}
			active[owner.EnvironmentID] = true
			running.Add(1)
			go func(owner store.EnvironmentInitialization) {
				defer running.Done()
				w.initializeEnvironment(ctx, owner)
				done <- owner.EnvironmentID
			}(owner)
		}
	}
}

func (w *Worker) initializeEnvironment(ctx context.Context, owner store.EnvironmentInitialization) {
	operation, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	failure := store.ProvisioningFailure{}
	err := w.prepareEnvironment(operation, owner, &failure)
	if err == nil {
		err = w.dispatcher.Store.CompleteEnvironmentInitialization(operation, owner)
	}
	if err != nil {
		log.Warn(ctx, "Environment preparation failed", "environment_id", owner.EnvironmentID, "session_id", owner.SessionID)
		// A later scan settles an unrecorded failure; it never retries the setup.
		record, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		_ = w.dispatcher.Store.FailEnvironmentInitialization(record, owner, failure)
	}
}

func (w *Worker) prepareEnvironment(ctx context.Context, owner store.EnvironmentInitialization, failure *store.ProvisioningFailure) error {
	environment, err := w.dispatcher.Store.GetEnvironment(ctx, owner.TenantID, owner.EnvironmentID)
	if err != nil {
		return err
	}
	var cfg struct {
		Files []store.InitialFileMetadata `json:"files"`
	}
	if json.Unmarshal(environment.Configuration, &cfg) != nil || len(cfg.Files) > 50 {
		return store.ErrInvalidInput
	}
	setup, err := w.dispatcher.Store.ReadEnvironmentSetup(ctx, owner.TenantID, owner.SessionID)
	if err != nil {
		return err
	}
	peer, err := w.dispatcher.authorizedPeer(ctx, owner.DeviceID)
	if err != nil {
		return err
	}
	identity := agentcapabilities.Identity{EnvironmentID: owner.EnvironmentID, SessionID: owner.SessionID}
	operations := setupOperations(setup)
	for index := 0; index < len(cfg.Files)+len(operations); index++ {
		step, stop := context.WithTimeout(ctx, 2*time.Minute)
		err = w.dispatcher.Store.CheckExecutionOwnership(step)
		if err == nil {
			var currentPeer = peer
			currentPeer, err = w.dispatcher.authorizedPeer(step, owner.DeviceID)
			if err == nil && currentPeer != peer {
				err = errors.New("Runtime connection changed during initialization")
			}
		}
		candidate := store.ProvisioningFailure{Step: store.ProvisioningInitialFile}
		if err == nil && index < len(cfg.Files) {
			var metadata store.InitialFileMetadata
			var body []byte
			metadata, body, err = w.dispatcher.Store.ReadInitialEnvironmentFile(step, owner.TenantID, owner.SessionID, index)
			if err == nil {
				err = installInitialFile(step, peer, identity, metadata, body)
			}
		} else if err == nil {
			command := operations[index-len(cfg.Files)]
			candidate = command.provisioningFailure(0)
			err = runRuntimeSetup(step, peer, identity, command)
		}
		stop()
		if err != nil {
			var confirmed *runtimeStepFailure
			if errors.As(err, &confirmed) {
				candidate.ExitCode = confirmed.exitCode
				*failure = candidate
			}
			return err
		}
	}
	return nil
}
